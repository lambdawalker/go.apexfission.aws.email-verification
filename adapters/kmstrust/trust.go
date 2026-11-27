// Package kmstrust validates a single explicitly trusted KMS key on each load.
// It does not implement key rotation, public archives, or historical revocations.
package kmstrust

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kt "github.com/aws/aws-sdk-go-v2/service/kms/types"
	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"time"
)

type API interface {
	DescribeKey(context.Context, *kms.DescribeKeyInput, ...func(*kms.Options)) (*kms.DescribeKeyOutput, error)
	GetPublicKey(context.Context, *kms.GetPublicKeyInput, ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error)
}
type pinnedTrust struct {
	Issuer, ID string
	Key        v.KeyTrust
}

func (p pinnedTrust) Resolve(_ context.Context, issuer, id string) (v.KeyTrust, error) {
	if issuer != p.Issuer || id != p.ID {
		return v.KeyTrust{}, v.ErrInvalid
	}
	return p.Key, nil
}

// Test trust pins one immutable stack key and checks KMS on every delivery.
// This is intentionally NOT the production archive/status/rotation protocol.
func Load(ctx context.Context, client API, arn, issuer string, c config.Config) (v.Trust, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if client == nil || arn == "" || issuer == "" {
		return nil, v.ErrInvalid
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSeconds)*time.Second)
	defer cancel()
	meta, e := client.DescribeKey(bounded, &kms.DescribeKeyInput{KeyId: aws.String(arn)})
	if e != nil {
		return nil, e
	}
	if meta == nil {
		return nil, v.ErrInvalid
	}
	k := meta.KeyMetadata
	if k == nil || aws.ToString(k.Arn) != arn || k.KeyState != kt.KeyStateEnabled || k.KeyUsage != kt.KeyUsageTypeSignVerify || k.KeySpec != kt.KeySpecEccNistP256 || k.CreationDate == nil {
		return nil, v.ErrInvalid
	}
	pub, e := client.GetPublicKey(bounded, &kms.GetPublicKeyInput{KeyId: aws.String(arn)})
	if e != nil {
		return nil, e
	}
	if pub == nil {
		return nil, v.ErrInvalid
	}
	if aws.ToString(pub.KeyId) != arn || pub.KeyUsage != kt.KeyUsageTypeSignVerify || pub.KeySpec != kt.KeySpecEccNistP256 {
		return nil, v.ErrInvalid
	}
	parsed, e := x509.ParsePKIXPublicKey(pub.PublicKey)
	if e != nil {
		return nil, e
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, v.ErrInvalid
	}
	from := k.CreationDate.UTC().Truncate(time.Second)
	until := from.Add(time.Duration(c.RotationSeconds) * time.Second)
	return pinnedTrust{issuer, aws.ToString(k.KeyId), v.KeyTrust{Public: key, State: "active", SigningFrom: from, SigningUntil: until, AcceptUntil: until.Add(time.Duration(c.KeyOverlapSeconds) * time.Second), CheckedAt: time.Now()}}, nil
}
