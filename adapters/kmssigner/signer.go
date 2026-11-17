// Package kmssigner converts KMS DER ECDSA signatures to JOSE ES256 format.
package kmssigner

import (
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/asn1"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"math/big"
	"strings"
)

type API interface {
	Sign(context.Context, *kms.SignInput, ...func(*kms.Options)) (*kms.SignOutput, error)
}
type Signer struct {
	Client  API
	ARN, ID string
}

func (s Signer) KeyID() string { return s.ID }
func (s Signer) Sign(ctx context.Context, input []byte) ([]byte, error) {
	if s.Client == nil || s.ID == "" || !strings.HasPrefix(s.ARN, "arn:") || !strings.Contains(s.ARN, ":key/") {
		return nil, errors.New("immutable KMS key ARN, client and kid required")
	}
	digest := sha256.Sum256(input)
	o, e := s.Client.Sign(ctx, &kms.SignInput{KeyId: aws.String(s.ARN), Message: digest[:], MessageType: types.MessageTypeDigest, SigningAlgorithm: types.SigningAlgorithmSpecEcdsaSha256})
	if e != nil {
		return nil, e
	}
	if o.SigningAlgorithm != types.SigningAlgorithmSpecEcdsaSha256 || o.KeyId == nil || *o.KeyId != s.ARN {
		return nil, errors.New("unexpected signing key or algorithm")
	}
	var sig struct{ R, S *big.Int }
	rest, e := asn1.Unmarshal(o.Signature, &sig)
	n := elliptic.P256().Params().N
	if e != nil || len(rest) != 0 || sig.R == nil || sig.S == nil || sig.R.Sign() <= 0 || sig.S.Sign() <= 0 || sig.R.Cmp(n) >= 0 || sig.S.Cmp(n) >= 0 {
		return nil, errors.New("invalid P-256 signature")
	}
	b := make([]byte, 64)
	sig.R.FillBytes(b[:32])
	sig.S.FillBytes(b[32:])
	return b, nil
}
