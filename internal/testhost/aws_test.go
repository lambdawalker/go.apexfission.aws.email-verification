package testhost

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kt "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
)

type emailCapture struct{ input *sesv2.SendEmailInput }

func (c *emailCapture) SendEmail(_ context.Context, in *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	c.input = in
	return &sesv2.SendEmailOutput{}, nil
}
func TestSESSendsCodeOnlyToConfiguredRecipient(t *testing.T) {
	api := &emailCapture{}
	m := Mailer{api, "sender@example.com", "recipient@example.com"}
	proof := v.Proofs{Link: "secret-link", Code: "123456"}
	if e := m.Send(context.Background(), "other@example.com", proof); e == nil || api.input != nil {
		t.Fatal("recipient restriction failed")
	}
	if e := m.Send(context.Background(), m.Recipient, proof); e != nil {
		t.Fatal(e)
	}
	text := aws.ToString(api.input.Content.Simple.Body.Text.Data)
	if !strings.Contains(text, proof.Code) || strings.Contains(text, proof.Link) {
		t.Fatal("wrong email content")
	}
}

type keyFake struct {
	arn         string
	state       kt.KeyState
	public      []byte
	publicCalls int
}

func (k *keyFake) DescribeKey(context.Context, *kms.DescribeKeyInput, ...func(*kms.Options)) (*kms.DescribeKeyOutput, error) {
	return &kms.DescribeKeyOutput{KeyMetadata: &kt.KeyMetadata{Arn: aws.String(k.arn), KeyId: aws.String("key-id"), KeyState: k.state, KeyUsage: kt.KeyUsageTypeSignVerify, KeySpec: kt.KeySpecEccNistP256, CreationDate: aws.Time(time.Now().Add(-time.Hour))}}, nil
}
func (k *keyFake) GetPublicKey(context.Context, *kms.GetPublicKeyInput, ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error) {
	k.publicCalls++
	return &kms.GetPublicKeyOutput{KeyId: aws.String(k.arn), KeyUsage: kt.KeyUsageTypeSignVerify, KeySpec: kt.KeySpecEccNistP256, PublicKey: k.public}, nil
}
func TestPinnedTrustRequiresEnabledExactKMSKey(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	k := &keyFake{arn: "arn:aws:kms:region:123:key/id", state: kt.KeyStateEnabled, public: der}
	trust, e := LoadTrust(context.Background(), k, k.arn, "issuer", config.Defaults())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = trust.Resolve(context.Background(), "other", "key-id"); e == nil {
		t.Fatal("wrong issuer accepted")
	}
	if _, e = trust.Resolve(context.Background(), "issuer", "other"); e == nil {
		t.Fatal("wrong key accepted")
	}
	if _, e = trust.Resolve(context.Background(), "issuer", "key-id"); e != nil {
		t.Fatal(e)
	}
	k.state = kt.KeyStateDisabled
	k.publicCalls = 0
	if _, e = LoadTrust(context.Background(), k, k.arn, "issuer", config.Defaults()); e == nil || k.publicCalls != 0 {
		t.Fatal("disabled key trusted")
	}
	k.state = kt.KeyStateEnabled
	if _, e = LoadTrust(context.Background(), k, "arn:wrong", "issuer", config.Defaults()); e == nil {
		t.Fatal("wrong KMS ARN trusted")
	}
}
