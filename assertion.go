package verification

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
	"io"
	"math/big"
	"strings"
	"time"
)

type Claims struct {
	Schema      int    `json:"schema_version"`
	Issuer      string `json:"iss"`
	Audience    string `json:"aud"`
	Email       string `json:"email"`
	Verified    bool   `json:"email_verified"`
	Nonce       string `json:"nonce"`
	Transaction string `json:"transaction_id"`
	VerifiedAt  int64  `json:"verified_at"`
	IssuedAt    int64  `json:"iat"`
	Expires     int64  `json:"exp"`
	ID          string `json:"jti"`
}
type Header struct {
	Type      string `json:"typ"`
	Algorithm string `json:"alg"`
	KeyID     string `json:"kid"`
}
type Signer interface {
	KeyID() string
	Sign(context.Context, []byte) ([]byte, error)
}
type LocalSigner struct {
	ID  string
	Key *ecdsa.PrivateKey
}

func (s LocalSigner) KeyID() string { return s.ID }
func (s LocalSigner) Sign(ctx context.Context, input []byte) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if s.Key == nil || s.Key.Curve != elliptic.P256() {
		return nil, ErrInvalid
	}
	h := sha256.Sum256(input)
	r, t, e := ecdsa.Sign(rand.Reader, s.Key, h[:])
	if e != nil {
		return nil, e
	}
	b := make([]byte, 64)
	r.FillBytes(b[:32])
	t.FillBytes(b[32:])
	return b, nil
}
func SignAssertion(ctx context.Context, s Signer, issuer string, t Transaction) (string, error) {
	if s == nil || s.KeyID() == "" || issuer == "" || t.AssertionID == "" || t.VerifiedAt.IsZero() || t.IssuedAt.IsZero() {
		return "", ErrInvalid
	}
	c := Claims{1, issuer, t.Request.Audience, t.Request.Email, true, t.Request.Nonce, t.ID, t.VerifiedAt.Unix(), t.IssuedAt.Unix(), t.ExpiresAt.Unix(), t.AssertionID}
	h, _ := json.Marshal(Header{"email-verification+jwt", "ES256", s.KeyID()})
	p, _ := json.Marshal(c)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	sig, e := s.Sign(ctx, []byte(input))
	if e != nil {
		return "", e
	}
	if len(sig) != 64 {
		return "", ErrInvalid
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// KeyTrust is supplied by an authoritative, freshness-enforcing trust provider.
// It is NOT populated from keys embedded in an incoming assertion.
type KeyTrust struct {
	Public                                            *ecdsa.PublicKey
	State                                             string
	SigningFrom, SigningUntil, AcceptUntil, CheckedAt time.Time
	Revoked                                           bool
}
type Trust interface {
	Resolve(context.Context, string, string) (KeyTrust, error)
}

// rejectDuplicates handles nested objects, including informational claim fields.
func rejectDuplicates(raw []byte) error {
	d := json.NewDecoder(strings.NewReader(string(raw)))
	var walk func() error
	walk = func() error {
		t, e := d.Token()
		if e != nil {
			return e
		}
		if x, ok := t.(json.Delim); ok {
			switch x {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					s, ok := k.(string)
					if !ok || seen[s] {
						return ErrInvalid
					}
					seen[s] = true
					if e = walk(); e != nil {
						return e
					}
				}
				_, e = d.Token()
				return e
			case '[':
				for d.More() {
					if e = walk(); e != nil {
						return e
					}
				}
				_, e = d.Token()
				return e
			default:
				return ErrInvalid
			}
		}
		return nil
	}
	if e := walk(); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrInvalid
	}
	return nil
}
func ValidateAssertion(ctx context.Context, token, issuer string, expected Request, transaction string, cfg config.Config, now time.Time, trust Trust) (Claims, error) {
	var c Claims
	if e := cfg.Validate(); e != nil {
		return c, e
	}
	if len(token) > 16384 || trust == nil {
		return c, ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, ErrInvalid
	}
	decoded := make([][]byte, 3)
	for i, p := range parts {
		b, e := base64.RawURLEncoding.DecodeString(p)
		if e != nil || base64.RawURLEncoding.EncodeToString(b) != p {
			return c, ErrInvalid
		}
		decoded[i] = b
	}
	if rejectDuplicates(decoded[0]) != nil || rejectDuplicates(decoded[1]) != nil {
		return c, ErrInvalid
	}
	var h Header
	d := json.NewDecoder(strings.NewReader(string(decoded[0])))
	d.DisallowUnknownFields()
	if d.Decode(&h) != nil || h.Type != "email-verification+jwt" || h.Algorithm != "ES256" || h.KeyID == "" {
		return c, ErrInvalid
	}
	if json.Unmarshal(decoded[1], &c) != nil {
		return c, ErrInvalid
	}
	if c.Schema != 1 || !c.Verified || c.Issuer != issuer || c.Audience != expected.Audience || c.Email != expected.Email || c.Nonce != expected.Nonce || c.Transaction != transaction || !validID(c.ID) || !validID(c.Nonce) || !validID(c.Transaction) {
		return c, ErrInvalid
	}
	if c.VerifiedAt <= 0 || c.IssuedAt < c.VerifiedAt || c.Expires <= c.IssuedAt || c.Expires-c.IssuedAt > cfg.AssertionSeconds {
		return c, ErrInvalid
	} // constrain Unix values before arithmetic
	if c.Expires > 253402300799 {
		return c, ErrInvalid
	}
	s := cfg.ToleranceSeconds
	n := now.Unix()
	if c.IssuedAt > n+s || c.VerifiedAt > n+s || n >= c.Expires+s || n >= c.VerifiedAt+cfg.VerificationAgeSeconds+s || !now.Before(expected.OperationDeadline) {
		return c, ErrExpired
	}
	k, e := trust.Resolve(ctx, issuer, h.KeyID)
	if e != nil {
		return c, e
	}
	if k.Revoked || (k.State != "active" && k.State != "retiring") || k.Public == nil || k.Public.Curve != elliptic.P256() || k.CheckedAt.After(now) || now.Sub(k.CheckedAt) >= time.Duration(cfg.MaxCacheSeconds)*time.Second {
		return c, ErrInvalid
	}
	issued := time.Unix(c.IssuedAt, 0)
	if issued.Before(k.SigningFrom) || !issued.Before(k.SigningUntil) || !now.Before(k.AcceptUntil) {
		return c, ErrExpired
	}
	if len(decoded[2]) != 64 {
		return c, ErrInvalid
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(k.Public, sum[:], new(big.Int).SetBytes(decoded[2][:32]), new(big.Int).SetBytes(decoded[2][32:])) {
		return c, ErrInvalid
	}
	return c, nil
}

// Accept is a trusted delivery entrypoint. Duplicate acceptance compares exact
// persisted bytes before time checks; it never executes work a second time.
func (h *Host) Accept(ctx context.Context, host, op, transaction, token, issuer string, trust Trust) error {
	k := key(host, op)
	for i := 0; i < 8; i++ {
		o, v, e := read[Operation](ctx, h.store, k)
		if e != nil {
			return e
		}
		if o.Accepted != "" {
			if o.Assertion == token {
				return nil
			}
			return ErrInvalid
		}
		if o.Blocked || !o.Dispatched || o.TransactionID == "" || o.TransactionID != transaction {
			return ErrState
		}
		c, e := ValidateAssertion(ctx, token, issuer, o.Request, transaction, h.cfg, h.now(), trust)
		if e != nil {
			return e
		}
		o.Accepted = c.ID
		o.Assertion = token
		o.WorkPending = true
		o.AcceptedAt = h.now()
		e = write(ctx, h.store, k, v, o)
		if errors.Is(e, storage.ErrConflict) {
			continue
		}
		return e
	}
	return storage.ErrConflict
}
