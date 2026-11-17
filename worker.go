package verification

import (
	"context"
	"crypto/rand"
	"errors"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
	"math/big"
	"time"
)

func nextAttempt(now time.Time, c config.Config, attempt int) (time.Time, error) {
	cap := c.BackoffSeconds
	for i := 1; i < attempt && cap < c.MaxBackoffSeconds; i++ {
		cap *= 2
		if cap > c.MaxBackoffSeconds {
			cap = c.MaxBackoffSeconds
		}
	}
	n, e := rand.Int(rand.Reader, big.NewInt(cap+1))
	if e != nil {
		return time.Time{}, e
	}
	return now.Add(time.Duration(c.TimeoutSeconds+n.Int64()) * time.Second), nil
}

// Issue runs one scheduled signing attempt. Hosts schedule another invocation at
// SignNext if it fails temporarily; a crash still consumes the persisted attempt.
func (s *Service) Issue(ctx context.Context, host, op, issuer string, signer Signer) (string, error) {
	t, e := s.ReserveIssue(ctx, host, op)
	if e != nil {
		return "", e
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(t.Policy.TimeoutSeconds)*time.Second)
	defer cancel()
	token, e := SignAssertion(bounded, signer, issuer, t)
	if e != nil {
		return "", e
	}
	if e = s.SaveResult(ctx, host, op, t.AssertionID, token); e != nil {
		if errors.Is(e, storage.ErrConflict) || errors.Is(e, ErrState) {
			current, _, err := read[Transaction](ctx, s.store, key(host, op))
			if err == nil && current.State == "ready" && current.AssertionID == t.AssertionID {
				return current.Assertion, nil
			}
		}
		return "", e
	}
	return token, nil
}

// Deliver invokes the trusted in-process host. An ambiguous error leaves the
// original persisted result intact. Expired duplicates can obtain a historical
// acknowledgment; Host.Accept will never perform a first expired acceptance.
func (s *Service) Deliver(ctx context.Context, host, op, issuer string, h *Host, trust Trust) error {
	if h == nil {
		return ErrInvalid
	}
	t, e := s.ReserveDelivery(ctx, host, op)
	if e != nil {
		return e
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(t.Policy.TimeoutSeconds)*time.Second)
	defer cancel()
	if e = h.Accept(bounded, host, op, t.ID, t.Assertion, issuer, trust); e != nil {
		return e
	}
	return s.Acknowledge(ctx, host, op, t.AssertionID)
}
