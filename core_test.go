package verification

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustID(t *testing.T) string {
	t.Helper()
	s, e := ID()
	if e != nil {
		t.Fatal(e)
	}
	return s
}

type fixture struct {
	host   *Host
	svc    *Service
	hs, vs *storage.Memory
	r      Request
	a      string
	now    time.Time
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{now: time.Unix(1800000000, 0), hs: storage.NewMemory(), vs: storage.NewMemory()}
	clock := func() time.Time { return f.now }
	var e error
	f.host, e = NewHost(f.hs, config.Defaults(), clock)
	if e != nil {
		t.Fatal(e)
	}
	f.svc, e = NewService(f.vs, config.Defaults(), clock, make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	f.a = mustID(t)
	f.r = Request{"host", mustID(t), "a@example.com", "aud", mustID(t), Challenge(f.a), f.now, f.now.Add(5 * time.Minute), f.now.Add(time.Hour)}
	if e = f.host.Prepare(context.Background(), f.r); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *fixture) start(t *testing.T) (Transaction, Proofs) {
	t.Helper()
	ctx := context.Background()
	p, e := f.host.Dispatch(ctx, f.r.Host, f.r.Operation)
	if e != nil {
		t.Fatal(e)
	}
	tr, proof, e := f.svc.Start(ctx, p, testProtector(t))
	if e != nil {
		t.Fatal(e)
	}
	if e = f.host.BindTransaction(ctx, f.r.Host, f.r.Operation, tr.ID); e != nil {
		t.Fatal(e)
	}
	return tr, proof
}
func TestOneUseDispatchAndMissingState(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	p, e := f.host.Dispatch(ctx, f.r.Host, f.r.Operation)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.host.Dispatch(ctx, f.r.Host, f.r.Operation); !errors.Is(e, ErrUncertain) {
		t.Fatal(e)
	}
	if _, e = f.svc.Lookup(ctx, f.r); !errors.Is(e, ErrUncertain) {
		t.Fatal(e)
	}
	if _, _, e = f.svc.Start(ctx, p, testProtector(t)); e != nil {
		t.Fatal(e)
	}
	if _, _, e = f.svc.Start(ctx, p, testProtector(t)); !errors.Is(e, ErrUncertain) {
		t.Fatal(e)
	} // a restart cannot regain the permit
	h, _ := NewHost(f.hs, config.Defaults(), func() time.Time { return f.now })
	if _, e = h.Dispatch(ctx, f.r.Host, f.r.Operation); !errors.Is(e, ErrUncertain) {
		t.Fatal(e)
	}
}
func TestConcurrentConfirmation(t *testing.T) {
	f := setup(t)
	tr, p := f.start(t)
	ctx := context.Background()
	var won atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := f.svc.Confirm(ctx, f.r.Host, f.r.Operation, tr.ID, f.a, p.Link, "")
			if e == nil {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("winners %d", won.Load())
	}
	r, e := f.svc.Lookup(ctx, f.r)
	if e != nil || r.State != "confirmed" || r.LinkHash != "" || r.CodeHash != "" {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestProofAndStartDeadlines(t *testing.T) {
	f := setup(t)
	tr, p := f.start(t)
	ctx := context.Background()
	if _, e := f.svc.Confirm(ctx, f.r.Host, f.r.Operation, tr.ID, "", p.Link, p.Code); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	f.now = tr.ProofDeadline
	if _, e := f.svc.Confirm(ctx, f.r.Host, f.r.Operation, tr.ID, f.a, "", p.Code); !errors.Is(e, ErrExpired) {
		t.Fatal(e)
	}
	if _, e := f.svc.Lookup(ctx, f.r); e != nil {
		t.Fatal(e)
	}
	g := setup(t)
	g.now = g.r.StartDeadline
	if _, e := g.host.Dispatch(ctx, g.r.Host, g.r.Operation); !errors.Is(e, ErrExpired) {
		t.Fatal(e)
	}
}

type fixedTrust struct{ k KeyTrust }

func (t fixedTrust) Resolve(context.Context, string, string) (KeyTrust, error) { return t.k, nil }
func TestLostAcknowledgmentAndFixedSigning(t *testing.T) {
	f := setup(t)
	tr, p := f.start(t)
	ctx := context.Background()
	if _, e := f.svc.Confirm(ctx, f.r.Host, f.r.Operation, tr.ID, f.a, "", p.Code); e != nil {
		t.Fatal(e)
	}
	first, e := f.svc.ReserveIssue(ctx, f.r.Host, f.r.Operation)
	if e != nil {
		t.Fatal(e)
	}
	f.now = f.now.Add(time.Minute)
	second, e := f.svc.ReserveIssue(ctx, f.r.Host, f.r.Operation)
	if e != nil || first.AssertionID != second.AssertionID || first.ExpiresAt != second.ExpiresAt || first.IssuedAt != second.IssuedAt {
		t.Fatal("claims renewed", e)
	}
	pk, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	token, e := SignAssertion(ctx, LocalSigner{"key", pk}, "issuer", second)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.svc.SaveResult(ctx, f.r.Host, f.r.Operation, second.AssertionID, token); e != nil {
		t.Fatal(e)
	}
	trust := fixedTrust{KeyTrust{&pk.PublicKey, "active", f.now.Add(-time.Hour), f.now.Add(time.Hour), f.now.Add(2 * time.Hour), f.now, false}}
	if e = f.host.Accept(ctx, f.r.Host, f.r.Operation, tr.ID, token, "issuer", trust); e != nil {
		t.Fatal(e)
	}
	f.now = f.now.Add(time.Hour)
	if e = f.host.Accept(ctx, f.r.Host, f.r.Operation, tr.ID, token, "issuer", trust); e != nil {
		t.Fatal("historical ack", e)
	}
	if e = f.svc.Acknowledge(ctx, f.r.Host, f.r.Operation, second.AssertionID); e != nil {
		t.Fatal(e)
	}
	got, e := f.svc.Lookup(ctx, f.r)
	if e != nil || got.Assertion != "" || got.CleanupAt.Before(f.now.Add(48*time.Hour)) {
		t.Fatal(got, e)
	}
}
func TestAttemptBudgetSurvivesRestart(t *testing.T) {
	f := setup(t)
	tr, p := f.start(t)
	ctx := context.Background()
	_, e := f.svc.Confirm(ctx, f.r.Host, f.r.Operation, tr.ID, f.a, p.Link, "")
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		f.now = f.now.Add(time.Minute)
		if _, e = f.svc.ReserveIssue(ctx, f.r.Host, f.r.Operation); e != nil {
			t.Fatal(e)
		}
	}
	s, _ := NewService(f.vs, config.Defaults(), func() time.Time { return f.now }, make([]byte, 32))
	if _, e = s.ReserveIssue(ctx, f.r.Host, f.r.Operation); !errors.Is(e, ErrAttempts) {
		t.Fatal(e)
	}
}
func TestTrustRollback(t *testing.T) {
	ctx := context.Background()
	s := storage.NewMemory()
	a := TrustState{2, hash("two"), []string{"revoked"}}
	if e := CommitTrust(ctx, s, "issuer", "source", a); e != nil {
		t.Fatal(e)
	}
	for _, b := range []TrustState{{1, hash("one"), nil}, {2, hash("different"), a.Revocations}, {3, hash("three"), nil}} {
		if e := CommitTrust(ctx, s, "issuer", "source", b); e == nil {
			t.Fatal("accepted rollback", b)
		}
	}
	if e := CommitTrust(ctx, s, "issuer", "source", a); e != nil {
		t.Fatal(e)
	}
}
func TestStatusIsolation(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	token, e := f.host.IssueStatus(ctx, f.r.Host, f.r.Operation)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.host.Status(ctx, f.r.Host, f.r.Operation, token); e != nil {
		t.Fatal(e)
	}
	if _, e = f.host.Status(ctx, f.r.Host, mustID(t), token); e == nil {
		t.Fatal("cross operation")
	}
	f.now = f.now.Add(30 * time.Minute)
	if _, e = f.host.Status(ctx, f.r.Host, f.r.Operation, token); e == nil {
		t.Fatal("expired status")
	}
}
func TestDuplicateJSON(t *testing.T) {
	if rejectDuplicates([]byte(`{"a":1,"a":2}`)) == nil {
		t.Fatal("duplicate accepted")
	}
	if rejectDuplicates([]byte(`{"a":{"b":1,"b":2}}`)) == nil {
		t.Fatal("nested duplicate")
	}
}

func testProtector(t *testing.T) Protector {
	t.Helper()
	p, e := NewAESProtector(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	return p
}

type captureMail struct {
	proof Proofs
	count int
	fail  bool
}

func (m *captureMail) Send(_ context.Context, _ string, p Proofs) error {
	m.count++
	m.proof = p
	if m.fail {
		return errors.New("provider timeout")
	}
	return nil
}
func TestDurableEmailRetry(t *testing.T) {
	f := setup(t)
	tr, proof := f.start(t)
	m := &captureMail{fail: true}
	ctx := context.Background()
	p := testProtector(t)
	if e := f.svc.SendMail(ctx, f.r.Host, f.r.Operation, p, m); e == nil {
		t.Fatal("expected provider failure")
	}
	f.now = f.now.Add(time.Minute)
	m.fail = false
	s, _ := NewService(f.vs, config.Defaults(), func() time.Time { return f.now }, make([]byte, 32))
	if e := s.SendMail(ctx, f.r.Host, f.r.Operation, p, m); e != nil {
		t.Fatal(e)
	}
	if m.proof != proof || m.count != 2 {
		t.Fatal("proof regenerated")
	}
	got, e := s.Lookup(ctx, f.r)
	if e != nil || len(got.MailPayload) != 0 || !got.MailSent || got.ID != tr.ID {
		t.Fatal(got, e)
	}
}
func TestBlockRejectsLateResult(t *testing.T) {
	f := setup(t)
	tr, p := f.start(t)
	ctx := context.Background()
	_, _ = f.svc.Confirm(ctx, f.r.Host, f.r.Operation, tr.ID, f.a, p.Link, "")
	r, _ := f.svc.ReserveIssue(ctx, f.r.Host, f.r.Operation)
	pk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	token, _ := SignAssertion(ctx, LocalSigner{"k", pk}, "issuer", r)
	if e := f.host.Block(ctx, f.r.Host, f.r.Operation); e != nil {
		t.Fatal(e)
	}
	if e := f.host.Accept(ctx, f.r.Host, f.r.Operation, tr.ID, token, "issuer", nil); !errors.Is(e, ErrState) {
		t.Fatal(e)
	}
}
func TestAssertionRejections(t *testing.T) {
	f := setup(t)
	tr, p := f.start(t)
	ctx := context.Background()
	_, e := f.svc.Confirm(ctx, f.r.Host, f.r.Operation, tr.ID, f.a, p.Link, "")
	if e != nil {
		t.Fatal(e)
	}
	r, e := f.svc.ReserveIssue(ctx, f.r.Host, f.r.Operation)
	if e != nil {
		t.Fatal(e)
	}
	pk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	token, e := SignAssertion(ctx, LocalSigner{"k", pk}, "issuer", r)
	if e != nil {
		t.Fatal(e)
	}
	k := KeyTrust{Public: &pk.PublicKey, State: "active", SigningFrom: f.now.Add(-time.Hour), SigningUntil: f.now.Add(time.Hour), AcceptUntil: f.now.Add(time.Hour), CheckedAt: f.now}
	for _, name := range []string{"revoked", "stale", "retired", "wrong-audience", "wrong-email", "wrong-transaction", "expired", "tampered"} {
		t.Run(name, func(t *testing.T) {
			kk := k
			req := f.r
			tx := tr.ID
			now := f.now
			tok := token
			switch name {
			case "revoked":
				kk.Revoked = true
			case "stale":
				kk.CheckedAt = now.Add(-15 * time.Minute)
			case "retired":
				kk.State = "retired"
			case "wrong-audience":
				req.Audience = "other"
			case "wrong-email":
				req.Email = "other@example.com"
			case "wrong-transaction":
				tx = mustID(t)
			case "expired":
				now = r.ExpiresAt.Add(3 * time.Second)
			case "tampered":
				tok = token[:len(token)-5] + "AAAAA"
			}
			if _, e := ValidateAssertion(ctx, tok, "issuer", req, tx, config.Defaults(), now, fixedTrust{kk}); e == nil {
				t.Fatal("accepted", name)
			}
		})
	}
}
