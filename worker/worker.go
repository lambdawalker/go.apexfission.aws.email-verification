// Package worker runs durable verification work independently of request handling.
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
)

// CursorPrefix reserves metadata keys in a verifier-owned store. Sources that
// scan that store must exclude these records from transaction decoding.
const CursorPrefix = "_verification-worker:"

type Source interface {
	Scan(context.Context, string) ([]v.Transaction, string, error)
}
type TrustLoader func(context.Context) (v.Trust, error)

// Delivery must durably accept and acknowledge the original result. It is a
// trusted host integration, never a callback URL supplied by a client.
type Delivery func(context.Context, v.Transaction) error

type Settings struct {
	Service        *v.Service
	Protector      v.Protector
	Mailer         v.Mailer
	Signer         v.Signer
	Deliver        Delivery
	Source         Source
	CursorStore    storage.Store
	HostID, Issuer string
	Policy         config.Config
	Now            func() time.Time
	MaxPages       int
}
type Worker struct {
	settings  Settings
	cursorKey string
}

func New(s Settings) (*Worker, error) {
	if err := s.Policy.Validate(); err != nil {
		return nil, err
	}
	if s.Service == nil || s.Protector == nil || s.Mailer == nil || s.Signer == nil || s.Deliver == nil || s.Source == nil || s.CursorStore == nil || s.HostID == "" || s.Issuer == "" {
		return nil, v.ErrInvalid
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.MaxPages == 0 {
		s.MaxPages = 20
	}
	if s.MaxPages < 1 || s.MaxPages > 100 {
		return nil, v.ErrInvalid
	}
	digest := sha256.Sum256([]byte(s.HostID + "\x00" + s.Issuer))
	return &Worker{settings: s, cursorKey: CursorPrefix + hex.EncodeToString(digest[:])}, nil
}

// InProcess connects the worker to a host's durable acceptance implementation.
// The host's records and trusted key policy stay owned by the consuming project.
func InProcess(service *v.Service, host *v.Host, issuer string, trust TrustLoader) Delivery {
	return func(ctx context.Context, tx v.Transaction) error {
		if service == nil || host == nil || trust == nil || issuer == "" {
			return v.ErrInvalid
		}
		if err := host.BindTransaction(ctx, tx.Request.Host, tx.Request.Operation, tx.ID); err != nil {
			return err
		}
		t, err := trust(ctx)
		if err != nil {
			return err
		}
		return service.Deliver(ctx, tx.Request.Host, tx.Request.Operation, issuer, host, t)
	}
}

// Step attempts one phase. Persisted CAS reservations and retry deadlines in the
// service arbitrate concurrent invocations; an email timeout may still duplicate mail.
func (w *Worker) Step(ctx context.Context, tx v.Transaction) error {
	s := w.settings
	if tx.Request.Host != s.HostID {
		return v.ErrInvalid
	}
	switch tx.State {
	case "pending":
		if !s.Now().Before(tx.ProofDeadline) {
			if len(tx.MailPayload) == 0 && tx.LinkHash == "" && tx.CodeHash == "" {
				return nil
			}
			return s.Service.PurgeEmailSecrets(ctx, s.HostID, tx.Request.Operation)
		}
		if tx.ProofAttempts >= tx.Policy.ProofAttempts {
			return nil
		}
		if !tx.MailSent && tx.MailAttempts < tx.Policy.Attempts && !s.Now().Before(tx.MailNext) {
			return s.Service.SendMail(ctx, s.HostID, tx.Request.Operation, s.Protector, s.Mailer)
		}
	case "confirmed":
		if tx.SignAttempts >= tx.Policy.Attempts || s.Now().Before(tx.SignNext) {
			return nil
		}
		_, err := s.Service.Issue(ctx, s.HostID, tx.Request.Operation, s.Issuer, s.Signer)
		return err
	case "ready":
		if tx.DeliveryAttempts >= tx.Policy.Attempts || s.Now().Before(tx.DeliveryNext) {
			return nil
		}
		return s.Deliver(ctx, tx)
	}
	return nil
}

// Tick checkpoints each completed page. An unfinished page is retried after a
// timeout. Cursor data belongs to the verifier, not the application's host store.
func (w *Worker) Tick(ctx context.Context) error {
	s := w.settings
	var transient error
	for page := 0; page < s.MaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return errors.Join(transient, err)
		}
		row, err := s.CursorStore.Load(ctx, w.cursorKey)
		var cursor string
		if err != nil && !errors.Is(err, storage.ErrMissing) {
			return err
		}
		if err == nil {
			if err = json.Unmarshal(row.Data, &cursor); err != nil {
				return err
			}
		}
		rows, next, err := s.Source.Scan(ctx, cursor)
		if err != nil {
			return err
		}
		for _, tx := range rows {
			if tx.Request.Host != s.HostID {
				continue
			}
			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < time.Duration(2*tx.Policy.TimeoutSeconds+5)*time.Second {
				return transient
			}
			err = w.Step(ctx, tx)
			if err != nil && !errors.Is(err, v.ErrState) && !errors.Is(err, v.ErrExpired) && !errors.Is(err, v.ErrAttempts) && !errors.Is(err, storage.ErrConflict) {
				transient = errors.Join(transient, err)
			}
		}
		raw, _ := json.Marshal(next)
		err = s.CursorStore.CompareAndSwap(ctx, w.cursorKey, row.Version, raw)
		if errors.Is(err, storage.ErrConflict) {
			return transient
		}
		if err != nil {
			return err
		}
		if next == "" {
			return transient
		}
	}
	return transient
}

// Handle is the worker-only Lambda handler. It never accepts API operations.
func (w *Worker) Handle(ctx context.Context, raw json.RawMessage) (map[string]string, error) {
	var event struct {
		Action string `json:"action"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil || decoder.Decode(new(any)) != io.EOF || event.Action != "tick" {
		return nil, errors.New("invalid worker event")
	}
	if err := w.Tick(ctx); err != nil {
		return nil, errors.New("verification worker failed")
	}
	return map[string]string{"state": "worker_complete"}, nil
}
