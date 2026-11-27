package awsruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	ddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"strings"
	"testing"
)

type fakeDynamo struct{}

func (fakeDynamo) GetItem(context.Context, *ddb.GetItemInput, ...func(*ddb.Options)) (*ddb.GetItemOutput, error) {
	panic("initialization must not read transactions")
}
func (fakeDynamo) PutItem(context.Context, *ddb.PutItemInput, ...func(*ddb.Options)) (*ddb.PutItemOutput, error) {
	panic("initialization must not write transactions")
}

type fakeSecret struct {
	raw   string
	calls int
	arn   string
}

func (f *fakeSecret) GetSecretValue(ctx context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	if _, ok := ctx.Deadline(); !ok {
		panic("unbounded secret retrieval")
	}
	f.calls++
	f.arn = aws.ToString(in.SecretId)
	return &secretsmanager.GetSecretValueOutput{SecretString: aws.String(f.raw)}, nil
}
func TestAPIInitializesWithoutWorkerOrHostCapabilities(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"hmac": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("h", 32))), "encryption": base64.StdEncoding.EncodeToString([]byte(strings.Repeat("e", 32)))})
	secret := &fakeSecret{raw: string(raw)}
	runtime, err := New(context.Background(), Settings{Table: "verifier", SecretARN: "secret", Policy: config.Defaults()}, Clients{Dynamo: fakeDynamo{}, Secrets: secret})
	if err != nil || runtime == nil || runtime.Service == nil || runtime.Protector == nil || secret.arn != "secret" {
		t.Fatalf("initialization: %v", err)
	}
	sealed, err := runtime.Protector.Seal([]byte("payload"), []byte("transaction"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Protector.Open(sealed, []byte("different transaction")); err == nil {
		t.Fatal("encryption not bound to transaction")
	}
}
func TestInvalidKeysFailClosed(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	for _, raw := range []string{`{}`, `{"hmac":"bad","encryption":"bad"}`, `{"hmac":"` + key + `","encryption":"` + key + `"}`} {
		if _, _, err := decodeKeys(raw); err == nil {
			t.Fatal("invalid key configuration accepted")
		}
	}
}
func TestConfigurationFailsBeforeReadingSecrets(t *testing.T) {
	secret := &fakeSecret{}
	_, err := New(context.Background(), Settings{SecretARN: "secret", Policy: config.Defaults()}, Clients{Dynamo: fakeDynamo{}, Secrets: secret})
	if err == nil || secret.calls != 0 {
		t.Fatal("invalid configuration reached AWS")
	}
	_, err = FromEnvironment(func(string) string { return "" })
	if err == nil {
		t.Fatal("missing policy silently defaulted")
	}
}
