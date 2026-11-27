// Package testhost contains the isolated email verification demo application.
package testhost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
)

type Input struct {
	Action       string    `json:"action"`
	Operation    string    `json:"operation,omitempty"`
	Email        string    `json:"email,omitempty"`
	Challenge    string    `json:"challenge,omitempty"`
	AuthorizedAt time.Time `json:"authorized_at,omitempty"`
	Secret       string    `json:"secret,omitempty"`
	Code         string    `json:"code,omitempty"`
}
type Output struct {
	Operation string `json:"operation,omitempty"`
	State     string `json:"state,omitempty"`
	Accepted  bool   `json:"accepted"`
	Code      string `json:"error,omitempty"`
}
type App struct {
	Host              *v.Host
	Service           *v.Service
	Store             storage.Store
	Protector         v.Protector
	Policy            config.Config
	HostID, Recipient string
	Now               func() time.Time
}

func validID(s string) bool {
	b, e := base64.RawURLEncoding.DecodeString(s)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == s
}
func errorCode(e error) string {
	switch {
	case e == nil:
		return ""
	case errors.Is(e, v.ErrInvalid):
		return "invalid"
	case errors.Is(e, v.ErrExpired):
		return "expired"
	case errors.Is(e, v.ErrUncertain), errors.Is(e, storage.ErrMissing):
		return "unresolved"
	case errors.Is(e, v.ErrAttempts):
		return "attempts_exhausted"
	case errors.Is(e, storage.ErrConflict), errors.Is(e, v.ErrState):
		return "retry_later"
	default:
		return "temporarily_unavailable"
	}
}

// Handle processes an operator request or a request from the authenticated mobile adapter. It never
// returns transaction internals, email proofs, assertions, or provider errors.
func (a *App) Handle(ctx context.Context, in Input) (Output, error) {
	if !validID(in.Operation) {
		return Output{Code: "invalid"}, nil
	}
	var out Output
	var err error
	switch in.Action {
	case "start":
		out, err = a.start(ctx, in)
	case "confirm":
		var req v.Request
		req, err = a.request(ctx, in.Operation)
		if err == nil {
			var t v.Transaction
			t, err = a.Service.Lookup(ctx, req)
			if err == nil {
				_, err = a.Service.Confirm(ctx, a.HostID, in.Operation, t.ID, in.Secret, "", in.Code)
			}
		}
		if err == nil {
			out = Output{Operation: in.Operation, State: "confirmed"}
		}
	case "status":
		out, err = a.status(ctx, in.Operation)
	default:
		err = v.ErrInvalid
	}
	out.Code = errorCode(err)
	return out, nil
}
func (a *App) request(ctx context.Context, op string) (v.Request, error) {
	var r v.Request
	row, e := a.Store.Load(ctx, "test-request:"+op)
	if e != nil {
		return r, e
	}
	e = json.Unmarshal(row.Data, &r)
	if e == nil && (r.Host != a.HostID || r.Operation != op) {
		e = v.ErrInvalid
	}
	return r, e
}
func (a *App) start(ctx context.Context, in Input) (Output, error) {
	if in.Email != a.Recipient || !validID(in.Challenge) || in.AuthorizedAt.IsZero() {
		return Output{}, v.ErrInvalid
	}
	req, e := a.request(ctx, in.Operation)
	if errors.Is(e, storage.ErrMissing) {
		now := a.Now()
		deadline := in.AuthorizedAt.Add(time.Duration(a.Policy.StartSeconds) * time.Second)
		if in.AuthorizedAt.After(now) || !now.Before(deadline) {
			return Output{}, v.ErrExpired
		}
		nonce, err := v.ID()
		if err != nil {
			return Output{}, err
		}
		req = v.Request{Host: a.HostID, Operation: in.Operation, Email: in.Email, Audience: a.HostID, Nonce: nonce, Challenge: in.Challenge, AuthorizedAt: in.AuthorizedAt, StartDeadline: deadline, OperationDeadline: in.AuthorizedAt.Add(time.Duration(a.Policy.StatusSeconds) * time.Second)}
		raw, _ := json.Marshal(req)
		e = a.Store.CompareAndSwap(ctx, "test-request:"+in.Operation, 0, raw)
		if errors.Is(e, storage.ErrConflict) {
			req, e = a.request(ctx, in.Operation)
		}
	}
	if e != nil {
		return Output{}, e
	}
	if req.Email != in.Email || req.Challenge != in.Challenge || !req.AuthorizedAt.Equal(in.AuthorizedAt) {
		return Output{}, v.ErrInvalid
	}
	// Persist a preparation marker before dispatch. If the host operation later
	// disappears, a retry must never recreate its acceptance authority.
	_, e = a.Store.Load(ctx, "test-prepared:"+in.Operation)
	if errors.Is(e, storage.ErrMissing) {
		e = a.Host.Prepare(ctx, req)
		if errors.Is(e, storage.ErrConflict) {
			return Output{}, v.ErrUncertain
		}
		if e != nil {
			return Output{}, e
		}
		e = a.Store.CompareAndSwap(ctx, "test-prepared:"+in.Operation, 0, []byte("prepared"))
		if e != nil {
			return Output{}, e
		}
	} else if e != nil {
		return Output{}, e
	}
	if _, e = a.Host.ProgressForHost(ctx, a.HostID, in.Operation); e != nil {
		if errors.Is(e, storage.ErrMissing) {
			e = v.ErrUncertain
		}
		return Output{}, e
	}
	permit, e := a.Host.Dispatch(ctx, a.HostID, in.Operation)
	var tx v.Transaction
	if e == nil {
		tx, _, e = a.Service.Start(ctx, permit, a.Protector)
	} else if errors.Is(e, v.ErrUncertain) {
		tx, e = a.Service.Lookup(ctx, req)
	}
	if e != nil {
		return Output{}, e
	}
	if e = a.Host.BindTransaction(ctx, a.HostID, in.Operation, tx.ID); e != nil {
		return Output{}, e
	}
	return Output{Operation: in.Operation, State: tx.State}, nil
}
func (a *App) status(ctx context.Context, op string) (Output, error) {
	req, e := a.request(ctx, op)
	if e != nil {
		return Output{}, e
	}
	tx, e := a.Service.Lookup(ctx, req)
	if e != nil {
		return Output{}, e
	}
	progress, e := a.Host.ProgressForHost(ctx, a.HostID, op)
	if e != nil {
		return Output{}, e
	}
	state := tx.State
	if state == "pending" && !a.Now().Before(tx.ProofDeadline) {
		state = "expired"
	}
	if state == "pending" && tx.ProofAttempts >= tx.Policy.ProofAttempts {
		state = "attempts_exhausted"
	}
	if state == "pending" && !tx.MailSent && tx.MailAttempts >= tx.Policy.Attempts {
		state = "mail_attempts_exhausted"
	}
	if state == "confirmed" && (!a.Now().Before(tx.VerifiedAt.Add(time.Duration(tx.Policy.VerificationAgeSeconds)*time.Second)) || (!tx.ExpiresAt.IsZero() && !a.Now().Before(tx.ExpiresAt))) {
		state = "expired"
	}
	if state == "confirmed" && tx.SignAttempts >= tx.Policy.Attempts {
		state = "sign_attempts_exhausted"
	}
	if state == "ready" && tx.DeliveryAttempts >= tx.Policy.Attempts {
		state = "unresolved_delivery"
	}
	return Output{Operation: op, State: state, Accepted: progress.Accepted}, nil
}
