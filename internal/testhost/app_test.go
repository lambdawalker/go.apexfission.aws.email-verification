package testhost

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
)

type mailbox struct {
	proof v.Proofs
	calls int
}

func (m *mailbox) Send(_ context.Context, _ string, p v.Proofs) error {
	m.proof = p
	m.calls++
	return nil
}

type fixture struct {
	app            *App
	mail           *mailbox
	host, verifier *storage.Memory
	secret         string
	input          Input
	now            time.Time
	key            *ecdsa.PrivateKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{host: storage.NewMemory(), verifier: storage.NewMemory(), mail: &mailbox{}, now: time.Now().UTC().Truncate(time.Second)}
	f.secret, _ = v.ID()
	op, _ := v.ID()
	f.key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	f.input = Input{Action: "start", Operation: op, Email: "test@example.com", Challenge: v.Challenge(f.secret), AuthorizedAt: f.now}
	f.restart(t)
	return f
}
func (f *fixture) restart(t *testing.T) {
	t.Helper()
	policy := config.Defaults()
	clock := func() time.Time { return f.now }
	host, e := v.NewHost(f.host, policy, clock)
	if e != nil {
		t.Fatal(e)
	}
	svc, e := v.NewService(f.verifier, policy, clock, []byte(strings.Repeat("h", 32)))
	if e != nil {
		t.Fatal(e)
	}
	protector, _ := v.NewAESProtector([]byte(strings.Repeat("e", 32)))
	f.app = &App{Host: host, Service: svc, Store: f.host, Protector: protector, Mailer: f.mail, Signer: v.LocalSigner{ID: "test-key", Key: f.key}, Policy: policy, HostID: "test", Issuer: "test-issuer", Recipient: f.input.Email, Now: clock}
	f.app.Trust = func(context.Context) (v.Trust, error) {
		return pinnedTrust{"test-issuer", "test-key", v.KeyTrust{Public: &f.key.PublicKey, State: "active", SigningFrom: f.now.Add(-time.Hour), SigningUntil: f.now.Add(time.Hour), AcceptUntil: f.now.Add(time.Hour), CheckedAt: f.now}}, nil
	}
}
func (f *fixture) tx(t *testing.T) v.Transaction {
	t.Helper()
	r, e := f.app.request(context.Background(), f.input.Operation)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := f.app.Service.Lookup(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	return tx
}
func TestEndToEndAcrossRestarts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	out, e := f.app.Handle(ctx, f.input)
	if e != nil || out.Code != "" {
		t.Fatalf("start: %+v %v", out, e)
	}
	first := f.tx(t)
	f.restart(t)
	again, _ := f.app.Handle(ctx, f.input)
	if again.Code != "" || f.tx(t).ID != first.ID {
		t.Fatal("retry recreated transaction")
	}
	if e = f.app.Step(ctx, f.tx(t)); e != nil {
		t.Fatal(e)
	}
	if f.mail.calls != 1 || len(f.mail.proof.Code) != 6 {
		t.Fatal("mail not delivered")
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), f.secret) || strings.Contains(string(raw), f.mail.proof.Code) || strings.Contains(string(raw), "Assertion") {
		t.Fatal("secret leaked")
	}
	f.restart(t)
	confirmed, _ := f.app.Handle(ctx, Input{Action: "confirm", Operation: f.input.Operation, Secret: f.secret, Code: f.mail.proof.Code})
	if confirmed.Code != "" {
		t.Fatal(confirmed)
	}
	if e = f.app.Step(ctx, f.tx(t)); e != nil {
		t.Fatal(e)
	}
	f.restart(t)
	if e = f.app.Step(ctx, f.tx(t)); e != nil {
		t.Fatal(e)
	}
	status, _ := f.app.Handle(ctx, Input{Action: "status", Operation: f.input.Operation})
	if !status.Accepted || status.State != "acknowledged" {
		t.Fatal(status)
	}
	if e = f.app.Step(ctx, f.tx(t)); e != nil {
		t.Fatal(e)
	}
	if f.mail.calls != 1 {
		t.Fatal("duplicate email after acknowledgment")
	}
}
func TestConflictsMissingStateAndExpiredStarts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.app.Handle(ctx, f.input)
	changed := f.input
	changed.Challenge = v.Challenge("different")
	out, _ := f.app.Handle(ctx, changed)
	if out.Code != "invalid" {
		t.Fatal(out)
	}
	// Lose only verifier data after dispatch: the durable host blocks recreation.
	f.verifier = storage.NewMemory()
	f.restart(t)
	out, _ = f.app.Handle(ctx, f.input)
	if out.Code != "unresolved" {
		t.Fatal(out)
	}
	f = newFixture(t)
	f.now = f.now.Add(6 * time.Minute)
	out, _ = f.app.Handle(ctx, f.input)
	if out.Code != "expired" {
		t.Fatal(out)
	}
}

type pages struct{ cursors []string }

func (p *pages) Scan(_ context.Context, c string) ([]v.Transaction, string, error) {
	p.cursors = append(p.cursors, c)
	if c == "" {
		return nil, "next-page", nil
	}
	return nil, "next-page", nil
}
func TestWorkerCursorSurvivesRestart(t *testing.T) {
	f := newFixture(t)
	p := &pages{}
	f.app.Scanner = p
	if e := f.app.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.restart(t)
	f.app.Scanner = p
	if e := f.app.Tick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(p.cursors) != 40 || p.cursors[20] != "next-page" {
		t.Fatal(p.cursors)
	}
}
func TestRecipientAndClientProofRequired(t *testing.T) {
	f := newFixture(t)
	in := f.input
	in.Email = "other@example.com"
	out, _ := f.app.Handle(context.Background(), in)
	if out.Code != "invalid" {
		t.Fatal(out)
	}
	f.app.Handle(context.Background(), f.input)
	f.app.Step(context.Background(), f.tx(t))
	out, _ = f.app.Handle(context.Background(), Input{Action: "confirm", Operation: in.Operation, Code: f.mail.proof.Code})
	if out.Code != "invalid" {
		t.Fatal("code alone confirmed")
	}
}

func TestMissingHostOperationIsNeverRecreated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	out, _ := f.app.Handle(ctx, f.input)
	if out.Code != "" {
		t.Fatal(out)
	}
	// Keep the durable request and dispatch marker but lose host operation data.
	empty := storage.NewMemory()
	f.app.Host, _ = v.NewHost(empty, f.app.Policy, f.app.Now)
	out, _ = f.app.Handle(ctx, f.input)
	if out.Code != "unresolved" {
		t.Fatal(out)
	}
	if _, e := f.app.Host.ProgressForHost(ctx, f.app.HostID, f.input.Operation); e != storage.ErrMissing {
		t.Fatal("host operation was recreated")
	}
}
