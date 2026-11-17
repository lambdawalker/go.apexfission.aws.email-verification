package verification

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
	"time"
)

// Protector must encrypt durably recoverable email secrets with a separately
// managed encryption key. Keep that key outside transaction storage.
type Protector interface {
	Seal([]byte, []byte) ([]byte, error)
	Open([]byte, []byte) ([]byte, error)
}
type AESProtector struct{ aead cipher.AEAD }

func NewAESProtector(key []byte) (*AESProtector, error) {
	if len(key) != 32 {
		return nil, ErrInvalid
	}
	b, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(b)
	return &AESProtector{a}, e
}
func (p *AESProtector) Seal(raw, aad []byte) ([]byte, error) {
	n := make([]byte, p.aead.NonceSize())
	if _, e := rand.Read(n); e != nil {
		return nil, e
	}
	return p.aead.Seal(n, n, raw, aad), nil
}
func (p *AESProtector) Open(raw, aad []byte) ([]byte, error) {
	n := p.aead.NonceSize()
	if len(raw) < n {
		return nil, ErrInvalid
	}
	return p.aead.Open(nil, raw[:n], raw[n:], aad)
}

type Mailer interface {
	Send(context.Context, string, Proofs) error
}

// SendMail recovers the same encrypted proof payload. Provider timeout may mean
// the email was sent; retries may duplicate email but never create new proofs.
func (s *Service) SendMail(ctx context.Context, host, op string, p Protector, m Mailer) error {
	if p == nil || m == nil {
		return ErrInvalid
	}
	k := key(host, op)
	t, v, e := read[Transaction](ctx, s.store, k)
	if e != nil {
		return e
	}
	if t.MailSent {
		return nil
	}
	if t.State != "pending" || !s.now().Before(t.ProofDeadline) {
		return ErrExpired
	}
	if t.MailAttempts >= t.Policy.Attempts {
		return ErrAttempts
	}
	if s.now().Before(t.MailNext) {
		return ErrState
	}
	t.MailAttempts++
	t.MailNext = s.now().Add(time.Duration(t.Policy.TimeoutSeconds+t.Policy.MaxBackoffSeconds) * time.Second)
	if e = write(ctx, s.store, k, v, t); e != nil {
		return e
	}
	raw, e := p.Open(t.MailPayload, []byte(t.ID))
	if e != nil {
		return e
	}
	var proofs Proofs
	if e = json.Unmarshal(raw, &proofs); e != nil {
		return e
	}
	sendCtx, cancel := context.WithTimeout(ctx, time.Duration(t.Policy.TimeoutSeconds)*time.Second)
	defer cancel()
	if e = m.Send(sendCtx, t.Request.Email, proofs); e != nil {
		return e
	}
	for i := 0; i < 8; i++ {
		current, version, e := read[Transaction](ctx, s.store, k)
		if e != nil {
			return e
		}
		current.MailSent = true
		current.MailPayload = nil
		e = write(ctx, s.store, k, version, current)
		if errors.Is(e, storage.ErrConflict) {
			continue
		}
		return e
	}
	return storage.ErrConflict
}

// PurgeEmailSecrets removes expired/consumed payloads without deleting tombstones.
func (s *Service) PurgeEmailSecrets(ctx context.Context, host, op string) error {
	k := key(host, op)
	t, v, e := read[Transaction](ctx, s.store, k)
	if e != nil {
		return e
	}
	if t.State == "pending" && s.now().Before(t.ProofDeadline) {
		return ErrState
	}
	t.MailPayload = nil
	t.LinkHash = ""
	t.CodeHash = ""
	return write(ctx, s.store, k, v, t)
}
