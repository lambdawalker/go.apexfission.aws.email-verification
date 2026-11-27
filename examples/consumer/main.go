// Consumer demonstrates a separate project integrating the public worker.
// It uses independent API/worker instances and makes no AWS calls.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/storage"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/worker"
	"time"
)

type mail struct{ proof v.Proofs }

func (m *mail) Send(_ context.Context, _ string, p v.Proofs) error { m.proof = p; return nil }

type trust struct{ k v.KeyTrust }

func (t trust) Resolve(context.Context, string, string) (v.KeyTrust, error) { return t.k, nil }
func check(e error) {
	if e != nil {
		panic(e)
	}
}
func id() string { x, e := v.ID(); check(e); return x }
func main() {
	ctx := context.Background()
	cfg := config.Defaults()
	now := time.Now
	h, e := v.NewHost(storage.NewMemory(), cfg, now)
	check(e)
	secret := make([]byte, 32)
	_, e = rand.Read(secret)
	check(e)
	verifierStore := storage.NewMemory()
	s, e := v.NewService(verifierStore, cfg, now, secret)
	check(e)
	encryption := make([]byte, 32)
	_, e = rand.Read(encryption)
	check(e)
	protector, e := v.NewAESProtector(encryption)
	check(e)
	a := id()
	start := now().UTC()
	r := v.Request{Host: "demo", Operation: id(), Email: "demo@example.com", Audience: "demo", Nonce: id(), Challenge: v.Challenge(a), AuthorizedAt: start, StartDeadline: start.Add(5 * time.Minute), OperationDeadline: start.Add(time.Hour)}
	check(h.Prepare(ctx, r))
	permit, e := h.Dispatch(ctx, r.Host, r.Operation)
	check(e)
	tx, _, e := s.Start(ctx, permit, protector)
	check(e)
	check(h.BindTransaction(ctx, r.Host, r.Operation, tx.ID))

	m := &mail{}
	pk, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(e)
	// A separately constructed service represents the worker process.
	service, e := v.NewService(verifierStore, cfg, now, secret)
	check(e)
	keys := trust{v.KeyTrust{Public: &pk.PublicKey, State: "active", SigningFrom: start.Add(-time.Hour), SigningUntil: start.Add(time.Hour), AcceptUntil: start.Add(2 * time.Hour), CheckedAt: now()}}
	source := transactionSource{service: service, request: r}
	w, e := worker.New(worker.Settings{Service: service, Protector: protector, Mailer: m, Signer: v.LocalSigner{ID: "demo-key", Key: pk},
		Deliver: worker.InProcess(service, h, "demo-issuer", func(context.Context) (v.Trust, error) { return keys, nil }), Source: source, CursorStore: verifierStore, HostID: r.Host, Issuer: "demo-issuer", Policy: cfg})
	check(e)
	check(w.Tick(ctx)) // sends pending mail
	_, e = s.Confirm(ctx, r.Host, r.Operation, tx.ID, a, "", m.proof.Code)
	check(e)
	check(w.Tick(ctx)) // signs confirmed evidence
	check(w.Tick(ctx)) // host acceptance and durable acknowledgment
	token, e := h.IssueStatus(ctx, r.Host, r.Operation)
	check(e)
	progress, e := h.Status(ctx, r.Host, r.Operation, token)
	check(e)
	fmt.Printf("Host accepted: %t; durable host work pending: %t\n", progress.Accepted, progress.WorkPending)
}

type transactionSource struct {
	service *v.Service
	request v.Request
}

func (s transactionSource) Scan(ctx context.Context, _ string) ([]v.Transaction, string, error) {
	tx, err := s.service.Lookup(ctx, s.request)
	return []v.Transaction{tx}, "", err
}
