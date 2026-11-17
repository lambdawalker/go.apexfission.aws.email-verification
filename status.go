package verification

import (
	"context"
	"time"
)

type statusCredential struct {
	Operation, Hash string
	Expires         time.Time
}

// IssueStatus is trusted host code. A lost response requires separately authorized
// replacement; there is no email/transaction-ID-only recovery API.
func (h *Host) IssueStatus(ctx context.Context, host, op string) (string, error) {
	if _, _, e := read[Operation](ctx, h.store, key(host, op)); e != nil {
		return "", e
	}
	token, e := ID()
	if e != nil {
		return "", e
	}
	row := statusCredential{op, hash(token), h.now().Add(time.Duration(h.cfg.StatusSeconds) * time.Second)}
	e = write(ctx, h.store, "status:"+key(host, hash(token)), 0, row)
	return token, e
}

type Progress struct{ Accepted, WorkPending, Blocked bool }

// Status deliberately exposes no assertion, account credential or mutation.
func (h *Host) Status(ctx context.Context, host, op, token string) (Progress, error) {
	var p Progress
	if !validID(token) {
		return p, ErrInvalid
	}
	r, _, e := read[statusCredential](ctx, h.store, "status:"+key(host, hash(token)))
	if e != nil {
		return p, e
	}
	if r.Operation != op || !equal(r.Hash, hash(token)) || !h.now().Before(r.Expires) {
		return p, ErrInvalid
	}
	o, _, e := read[Operation](ctx, h.store, key(host, op))
	if e != nil {
		return p, e
	}
	return Progress{o.Accepted != "", o.WorkPending, o.Blocked}, nil
}
