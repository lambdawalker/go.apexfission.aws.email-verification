// Package verification implements in-process verification workflow primitives.
// Public service methods are called by trusted host code, never exposed as raw RPCs.
package verification

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
	"math/big"
	"sync/atomic"
	"time"
)

var (
	ErrInvalid   = errors.New("invalid input or proof")
	ErrExpired   = errors.New("expired")
	ErrUncertain = errors.New("uncertain dispatch: reconcile, do not recreate")
	ErrState     = errors.New("invalid workflow state")
	ErrAttempts  = errors.New("attempt budget exhausted")
)

func ID() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b), e
}
func validID(s string) bool {
	b, e := base64.RawURLEncoding.DecodeString(s)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}
func hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func Challenge(secret string) string { return hash(secret) }
func equal(a, b string) bool         { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func key(scope, id string) string    { return hash(scope + "\x00" + id) }
func read[T any](ctx context.Context, s storage.Store, k string) (T, uint64, error) {
	var t T
	r, e := s.Load(ctx, k)
	if e != nil {
		return t, 0, e
	}
	e = json.Unmarshal(r.Data, &t)
	return t, r.Version, e
}
func write(ctx context.Context, s storage.Store, k string, v uint64, t any) error {
	b, e := json.Marshal(t)
	if e != nil {
		return e
	}
	return s.CompareAndSwap(ctx, k, v, b)
}

type Request struct {
	Host, Operation, Email, Audience, Nonce, Challenge string
	AuthorizedAt, StartDeadline, OperationDeadline     time.Time
}

func (r Request) validate(c config.Config, now time.Time) error {
	if r.Host == "" || r.Email == "" || len(r.Email) > 320 || r.Audience == "" || !validID(r.Operation) || !validID(r.Nonce) || !validID(r.Challenge) {
		return ErrInvalid
	}
	if r.AuthorizedAt.After(now) || !r.StartDeadline.After(r.AuthorizedAt) || r.StartDeadline.Sub(r.AuthorizedAt) > time.Duration(c.StartSeconds)*time.Second || !r.OperationDeadline.After(now) {
		return ErrExpired
	}
	return nil
}

type Operation struct {
	TransactionID string
	Request       Request
	Dispatched    bool
	Blocked       bool
	Accepted      string
	Assertion     string
	WorkPending   bool
	AcceptedAt    time.Time
}

// Permit cannot be constructed outside this package and is consumed once in memory.
// A restart loses it deliberately. Host dispatch state must be durable.
type Permit struct {
	request Request
	used    atomic.Bool
}
type Host struct {
	store storage.Store
	cfg   config.Config
	now   func() time.Time
}

func NewHost(s storage.Store, c config.Config, now func() time.Time) (*Host, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if s == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Host{s, c, now}, nil
}

// Prepare must be invoked only after host eligibility and caller authorization.
func (h *Host) Prepare(ctx context.Context, r Request) error {
	if e := r.validate(h.cfg, h.now()); e != nil {
		return e
	}
	return write(ctx, h.store, key(r.Host, r.Operation), 0, Operation{Request: r})
}
func (h *Host) Dispatch(ctx context.Context, host, op string) (*Permit, error) {
	k := key(host, op)
	o, v, e := read[Operation](ctx, h.store, k)
	if e != nil {
		return nil, e
	}
	if o.Dispatched || o.Blocked {
		return nil, ErrUncertain
	}
	if !h.now().Before(o.Request.StartDeadline) {
		return nil, ErrExpired
	}
	o.Dispatched = true
	if e = write(ctx, h.store, k, v, o); e != nil {
		return nil, e
	}
	return &Permit{request: o.Request}, nil
}
func (h *Host) Block(ctx context.Context, host, op string) error {
	k := key(host, op)
	o, v, e := read[Operation](ctx, h.store, k)
	if e != nil {
		return e
	}
	if o.Accepted != "" {
		return ErrState
	}
	o.Blocked = true
	return write(ctx, h.store, k, v, o)
}

type Transaction struct {
	SignNext, DeliveryNext                                  time.Time
	Policy                                                  config.Config
	MailPayload                                             []byte
	MailSent                                                bool
	MailAttempts                                            int
	MailNext                                                time.Time
	Request                                                 Request
	ID, State, LinkHash, CodeHash                           string
	Created, ProofDeadline, VerifiedAt, IssuedAt, ExpiresAt time.Time
	AssertionID, Assertion                                  string
	ProofAttempts, SignAttempts, DeliveryAttempts           int
	ResolvedAt, CleanupAt                                   time.Time
}
type Proofs struct{ Link, Code string }
type Service struct {
	store   storage.Store
	cfg     config.Config
	now     func() time.Time
	hmacKey []byte
}

func NewService(s storage.Store, c config.Config, now func() time.Time, secret []byte) (*Service, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if s == nil || now == nil || len(secret) < 32 {
		return nil, ErrInvalid
	}
	return &Service{s, c, now, append([]byte(nil), secret...)}, nil
}
func (s *Service) codeHash(id, code string) string {
	m := hmac.New(sha256.New, s.hmacKey)
	m.Write([]byte(id + "\x00" + code))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Start consumes a trusted one-use dispatch permit. Proofs are returned only to
// the host's email adapter, never the client. Durable email transport is separate.
func (s *Service) Start(ctx context.Context, p *Permit, protector Protector) (Transaction, Proofs, error) {
	var t Transaction
	var proofs Proofs
	if protector == nil {
		return t, proofs, ErrInvalid
	}
	if p == nil || !p.used.CompareAndSwap(false, true) {
		return t, proofs, ErrUncertain
	}
	r := p.request
	if e := r.validate(s.cfg, s.now()); e != nil {
		return t, proofs, e
	}
	if !s.now().Before(r.StartDeadline) {
		return t, proofs, ErrExpired
	}
	id, e := ID()
	if e != nil {
		return t, proofs, e
	}
	link, e := ID()
	if e != nil {
		return t, proofs, e
	}
	n, e := rand.Int(rand.Reader, big.NewInt(1000000))
	if e != nil {
		return t, proofs, e
	}
	proofs = Proofs{link, fmt.Sprintf("%06d", n.Int64())}
	now := s.now()
	t = Transaction{Policy: s.cfg, Request: r, ID: id, State: "pending", Created: now, ProofDeadline: now.Add(time.Duration(s.cfg.ProofSeconds) * time.Second), LinkHash: hash(link), CodeHash: s.codeHash(id, proofs.Code)}
	raw, e := json.Marshal(proofs)
	if e != nil {
		return Transaction{}, Proofs{}, e
	}
	t.MailPayload, e = protector.Seal(raw, []byte(t.ID))
	if e != nil {
		return Transaction{}, Proofs{}, e
	}
	e = write(ctx, s.store, key(r.Host, r.Operation), 0, t)
	if e != nil {
		return Transaction{}, Proofs{}, e
	}
	return t, proofs, nil
}

// Lookup is a trusted-host retry/status API; it never creates missing records.
func (s *Service) Lookup(ctx context.Context, r Request) (Transaction, error) {
	t, _, e := read[Transaction](ctx, s.store, key(r.Host, r.Operation))
	if errors.Is(e, storage.ErrMissing) {
		return t, ErrUncertain
	}
	if e == nil && !sameRequest(t.Request, r) {
		return Transaction{}, ErrInvalid
	}
	return t, e
}
func (s *Service) Confirm(ctx context.Context, host, op, id, a, link, code string) (Transaction, error) {
	k := key(host, op)
	for i := 0; i < 8; i++ {
		t, v, e := read[Transaction](ctx, s.store, k)
		if e != nil {
			return t, e
		}
		if t.ID != id || !validID(a) || !equal(hash(a), t.Request.Challenge) {
			return Transaction{}, ErrInvalid
		}
		if t.State != "pending" {
			return Transaction{}, ErrState
		}
		if !s.now().Before(t.ProofDeadline) {
			return Transaction{}, ErrExpired
		}
		if t.ProofAttempts >= t.Policy.ProofAttempts {
			return Transaction{}, ErrAttempts
		}
		ok := (link != "" && code == "" && equal(hash(link), t.LinkHash)) || (code != "" && link == "" && equal(s.codeHash(t.ID, code), t.CodeHash))
		t.ProofAttempts++
		if ok {
			t.State = "confirmed"
			t.VerifiedAt = s.now().UTC().Truncate(time.Second)
			t.LinkHash = ""
			t.CodeHash = ""
			t.MailPayload = nil
		}
		e = write(ctx, s.store, k, v, t)
		if errors.Is(e, storage.ErrConflict) {
			continue
		}
		if e != nil {
			return Transaction{}, e
		}
		if !ok {
			return Transaction{}, ErrInvalid
		}
		return t, nil
	}
	return Transaction{}, storage.ErrConflict
}

// ReserveIssue atomically fixes assertion claims and consumes an attempt before
// external signing. A caller must persist output before returning/delivering it.
func (s *Service) ReserveIssue(ctx context.Context, host, op string) (Transaction, error) {
	k := key(host, op)
	t, v, e := read[Transaction](ctx, s.store, k)
	if e != nil {
		return t, e
	}
	if t.State != "confirmed" {
		return t, ErrState
	}
	now := s.now()
	if !now.Before(t.VerifiedAt.Add(time.Duration(t.Policy.VerificationAgeSeconds) * time.Second)) {
		return t, ErrExpired
	}
	if t.SignAttempts >= t.Policy.Attempts {
		return t, ErrAttempts
	}
	if t.AssertionID == "" {
		t.AssertionID, e = ID()
		if e != nil {
			return t, e
		}
		t.IssuedAt = now.UTC().Truncate(time.Second)
		t.ExpiresAt = t.IssuedAt.Add(time.Duration(t.Policy.AssertionSeconds) * time.Second)
	}
	if !now.Before(t.ExpiresAt) {
		return t, ErrExpired
	}
	if now.Before(t.SignNext) {
		return t, ErrState
	}
	t.SignAttempts++
	t.SignNext, e = nextAttempt(now, t.Policy, t.SignAttempts)
	if e != nil {
		return t, e
	}
	e = write(ctx, s.store, k, v, t)
	return t, e
}
func (s *Service) SaveResult(ctx context.Context, host, op, assertionID, token string) error {
	if token == "" || len(token) > 16384 {
		return ErrInvalid
	}
	k := key(host, op)
	t, v, e := read[Transaction](ctx, s.store, k)
	if e != nil {
		return e
	}
	if t.State != "confirmed" || t.AssertionID != assertionID || t.SignAttempts == 0 {
		return ErrState
	}
	t.Assertion = token
	t.State = "ready"
	return write(ctx, s.store, k, v, t)
}
func (s *Service) ReserveDelivery(ctx context.Context, host, op string) (Transaction, error) {
	k := key(host, op)
	t, v, e := read[Transaction](ctx, s.store, k)
	if e != nil {
		return t, e
	}
	if t.State != "ready" {
		return t, ErrState
	}
	if t.DeliveryAttempts >= t.Policy.Attempts {
		return t, ErrAttempts
	}
	if s.now().Before(t.DeliveryNext) {
		return t, ErrState
	}
	t.DeliveryAttempts++
	t.DeliveryNext, e = nextAttempt(s.now(), t.Policy, t.DeliveryAttempts)
	if e != nil {
		return t, e
	}
	e = write(ctx, s.store, k, v, t)
	return t, e
}
func (s *Service) Acknowledge(ctx context.Context, host, op, assertionID string) error {
	k := key(host, op)
	t, v, e := read[Transaction](ctx, s.store, k)
	if e != nil {
		return e
	}
	if t.AssertionID != assertionID {
		return ErrInvalid
	}
	if t.State == "acknowledged" {
		return nil
	}
	if t.State != "ready" {
		return ErrState
	}
	t.State = "acknowledged"
	t.ResolvedAt = s.now()
	t.CleanupAt, e = config.CleanupAt(t.ResolvedAt, t.Request.StartDeadline, t.Created.Add(time.Duration(t.Policy.RetryWindowSeconds)*time.Second), t.Policy.RetentionSeconds)
	if e != nil {
		return e
	}
	t.Assertion = ""
	return write(ctx, s.store, k, v, t)
}

// BindTransaction records the verifier transaction before acceptance is possible.
func (h *Host) BindTransaction(ctx context.Context, host, op, id string) error {
	if !validID(id) {
		return ErrInvalid
	}
	k := key(host, op)
	o, v, e := read[Operation](ctx, h.store, k)
	if e != nil {
		return e
	}
	if o.Blocked || !o.Dispatched {
		return ErrState
	}
	if o.TransactionID != "" {
		if o.TransactionID == id {
			return nil
		}
		return ErrInvalid
	}
	o.TransactionID = id
	return write(ctx, h.store, k, v, o)
}

func sameRequest(a, b Request) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return equal(hash(string(x)), hash(string(y)))
}
