package testhost

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dt "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	kt "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	st "github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	store "github.com/lambdawalker/go.apexfission.aws.email-verification/adapters/dynamodb"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/adapters/kmssigner"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
)

type SES interface {
	SendEmail(context.Context, *sesv2.SendEmailInput, ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)
}
type Mailer struct {
	Client            SES
	Sender, Recipient string
}

func (m Mailer) Send(ctx context.Context, email string, p v.Proofs) error {
	if email != m.Recipient || len(p.Code) != 6 {
		return v.ErrInvalid
	}
	_, e := m.Client.SendEmail(ctx, &sesv2.SendEmailInput{FromEmailAddress: aws.String(m.Sender), Destination: &st.Destination{ToAddresses: []string{email}}, Content: &st.EmailContent{Simple: &st.Message{Subject: &st.Content{Data: aws.String("Email verification test code"), Charset: aws.String("UTF-8")}, Body: &st.Body{Text: &st.Content{Data: aws.String("Your verification code is: " + p.Code + "\nReturn to the test client to confirm. Ignore this email if you did not request it."), Charset: aws.String("UTF-8")}}}}})
	return e
}

type ScanAPI interface {
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}
type DynamoScanner struct {
	Client ScanAPI
	Table  string
}

func (s DynamoScanner) Scan(ctx context.Context, cursor string) ([]v.Transaction, string, error) {
	in := &dynamodb.ScanInput{TableName: aws.String(s.Table), ConsistentRead: aws.Bool(true), Limit: aws.Int32(5)}
	if cursor != "" {
		in.ExclusiveStartKey = map[string]dt.AttributeValue{"pk": &dt.AttributeValueMemberS{Value: cursor}}
	}
	out, e := s.Client.Scan(ctx, in)
	if e != nil {
		return nil, "", e
	}
	rows := make([]v.Transaction, 0, len(out.Items))
	for _, item := range out.Items {
		data, ok := item["data"].(*dt.AttributeValueMemberB)
		if !ok {
			return nil, "", errors.New("invalid transaction row")
		}
		var tx v.Transaction
		if e = json.Unmarshal(data.Value, &tx); e != nil {
			return nil, "", e
		}
		if tx.ID == "" || tx.Request.Operation == "" {
			return nil, "", v.ErrInvalid
		}
		rows = append(rows, tx)
	}
	next := ""
	if len(out.LastEvaluatedKey) > 0 {
		key, ok := out.LastEvaluatedKey["pk"].(*dt.AttributeValueMemberS)
		if !ok {
			return nil, "", v.ErrInvalid
		}
		next = key.Value
	}
	return rows, next, nil
}

type KeyAPI interface {
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
func LoadTrust(ctx context.Context, client KeyAPI, arn, issuer string, c config.Config) (v.Trust, error) {
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.TimeoutSeconds)*time.Second)
	defer cancel()
	meta, e := client.DescribeKey(bounded, &kms.DescribeKeyInput{KeyId: aws.String(arn)})
	if e != nil {
		return nil, e
	}
	k := meta.KeyMetadata
	if k == nil || aws.ToString(k.Arn) != arn || k.KeyState != kt.KeyStateEnabled || k.KeyUsage != kt.KeyUsageTypeSignVerify || k.KeySpec != kt.KeySpecEccNistP256 || k.CreationDate == nil {
		return nil, v.ErrInvalid
	}
	pub, e := client.GetPublicKey(bounded, &kms.GetPublicKeyInput{KeyId: aws.String(arn)})
	if e != nil {
		return nil, e
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
func FromEnvironment(ctx context.Context) (*App, error) {
	names := []string{"HOST_TABLE", "VERIFIER_TABLE", "KEY_ARN", "KEY_ID", "SECRET_ARN", "HOST_ID", "SENDER", "RECIPIENT", "EMAIL_VERIFICATION_POLICY"}
	env := map[string]string{}
	for _, name := range names {
		env[name] = os.Getenv(name)
		if env[name] == "" {
			return nil, errors.New("missing test host configuration")
		}
	}
	if env["HOST_TABLE"] == env["VERIFIER_TABLE"] {
		return nil, errors.New("test tables must be separate")
	}
	policy, e := config.Load(strings.NewReader(env["EMAIL_VERIFICATION_POLICY"]))
	if e != nil {
		return nil, e
	}
	if policy.TimeoutSeconds > 30 {
		return nil, errors.New("test host timeout must be at most 30 seconds")
	}
	cfg, e := awsconfig.LoadDefaultConfig(ctx)
	if e != nil {
		return nil, e
	}
	ddb := dynamodb.NewFromConfig(cfg)
	keyClient := kms.NewFromConfig(cfg)
	secret, e := secretsmanager.NewFromConfig(cfg).GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(env["SECRET_ARN"])})
	if e != nil {
		return nil, e
	}
	var keys struct {
		HMAC       string `json:"hmac"`
		Encryption string `json:"encryption"`
	}
	if e = json.Unmarshal([]byte(aws.ToString(secret.SecretString)), &keys); e != nil {
		return nil, e
	}
	hmac, e := base64.StdEncoding.DecodeString(keys.HMAC)
	if e != nil || len(hmac) != 32 {
		return nil, v.ErrInvalid
	}
	encryption, e := base64.StdEncoding.DecodeString(keys.Encryption)
	if e != nil || len(encryption) != 32 || keys.HMAC == keys.Encryption {
		return nil, v.ErrInvalid
	}
	protector, e := v.NewAESProtector(encryption)
	if e != nil {
		return nil, e
	}
	hostStore := store.Store{Client: ddb, Table: env["HOST_TABLE"]}
	verifierStore := store.Store{Client: ddb, Table: env["VERIFIER_TABLE"]}
	h, e := v.NewHost(hostStore, policy, time.Now)
	if e != nil {
		return nil, e
	}
	svc, e := v.NewService(verifierStore, policy, time.Now, hmac)
	if e != nil {
		return nil, e
	}
	issuer := "urn:email-verification:test:" + env["HOST_ID"]
	return &App{Host: h, Service: svc, Store: hostStore, Protector: protector, Mailer: Mailer{sesv2.NewFromConfig(cfg), env["SENDER"], env["RECIPIENT"]}, Signer: kmssigner.Signer{Client: keyClient, ARN: env["KEY_ARN"], ID: env["KEY_ID"]}, Trust: func(ctx context.Context) (v.Trust, error) {
		return LoadTrust(ctx, keyClient, env["KEY_ARN"], issuer, policy)
	}, Scanner: DynamoScanner{ddb, env["VERIFIER_TABLE"]}, Policy: policy, HostID: env["HOST_ID"], Issuer: issuer, Recipient: env["RECIPIENT"], Now: time.Now}, nil
}
