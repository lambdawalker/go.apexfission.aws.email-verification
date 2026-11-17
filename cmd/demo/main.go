// Demo uses ephemeral keys and memory storage. It performs no AWS calls.
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
	s, e := v.NewService(storage.NewMemory(), cfg, now, secret)
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
	check(s.SendMail(ctx, r.Host, r.Operation, protector, m))
	_, e = s.Confirm(ctx, r.Host, r.Operation, tx.ID, a, "", m.proof.Code)
	check(e)
	pk, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	check(e)
	_, e = s.Issue(ctx, r.Host, r.Operation, "demo-issuer", v.LocalSigner{ID: "demo-key", Key: pk})
	check(e)
	keys := trust{v.KeyTrust{Public: &pk.PublicKey, State: "active", SigningFrom: start.Add(-time.Hour), SigningUntil: start.Add(time.Hour), AcceptUntil: start.Add(2 * time.Hour), CheckedAt: now()}}
	check(s.Deliver(ctx, r.Host, r.Operation, "demo-issuer", h, keys))
	token, e := h.IssueStatus(ctx, r.Host, r.Operation)
	check(e)
	progress, e := h.Status(ctx, r.Host, r.Operation, token)
	check(e)
	fmt.Printf("Host accepted: %t; durable host work pending: %t\n", progress.Accepted, progress.WorkPending)
}
