package kmssigner

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
	"math/big"
	"testing"
)

type fake struct {
	key *ecdsa.PrivateKey
	t   *testing.T
}

func (f fake) Sign(_ context.Context, in *kms.SignInput, _ ...func(*kms.Options)) (*kms.SignOutput, error) {
	if in.MessageType != types.MessageTypeDigest || len(in.Message) != 32 {
		f.t.Fatal("double hashing risk")
	}
	b, e := ecdsa.SignASN1(rand.Reader, f.key, in.Message)
	return &kms.SignOutput{KeyId: in.KeyId, SigningAlgorithm: in.SigningAlgorithm, Signature: b}, e
}
func TestDERToJOSE(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	s := Signer{fake{k, t}, "arn:aws:kms:us-east-1:123456789012:key/example", "kid"}
	input := []byte("encoded.header.payload")
	b, e := s.Sign(context.Background(), input)
	if e != nil || len(b) != 64 {
		t.Fatal(e)
	}
	d := sha256.Sum256(input)
	if !ecdsa.Verify(&k.PublicKey, d[:], new(big.Int).SetBytes(b[:32]), new(big.Int).SetBytes(b[32:])) {
		t.Fatal("invalid JOSE signature")
	}
}
