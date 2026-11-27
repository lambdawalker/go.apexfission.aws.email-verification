// Package awsruntime wires AWS clients to the public verification library.
// It never creates a host, chooses eligibility, or establishes key trust.
package awsruntime

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ddb "github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	store "github.com/lambdawalker/go.apexfission.aws.email-verification/adapters/dynamodb"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/adapters/kmssigner"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/adapters/sesmailer"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/worker"
)

type Settings struct {
	Table, SecretARN string
	Policy           config.Config
}

func (s Settings) Validate() error {
	if err := s.Policy.Validate(); err != nil {
		return err
	}
	if s.Table == "" || s.SecretARN == "" {
		return v.ErrInvalid
	}
	return nil
}
func FromEnvironment(get func(string) string) (Settings, error) {
	raw := get("EMAIL_VERIFICATION_POLICY")
	if raw == "" {
		return Settings{}, errors.New("missing EMAIL_VERIFICATION_POLICY")
	}
	p, err := config.Load(strings.NewReader(raw))
	if err != nil {
		return Settings{}, err
	}
	s := Settings{Table: get("VERIFIER_TABLE"), SecretARN: get("SECRET_ARN"), Policy: p}
	return s, s.Validate()
}

type SecretAPI interface {
	GetSecretValue(context.Context, *secretsmanager.GetSecretValueInput, ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}
type Clients struct {
	Dynamo  store.API
	Secrets SecretAPI
}
type Runtime struct {
	Service   *v.Service
	Protector v.Protector
	Store     store.Store
	policy    config.Config
}

// New initializes the API's capabilities only: storage and proof protection.
// It does not request KMS signing, SES, scanning, or access to host storage.
func New(ctx context.Context, s Settings, c Clients) (*Runtime, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if c.Dynamo == nil || c.Secrets == nil {
		return nil, v.ErrInvalid
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.Policy.TimeoutSeconds)*time.Second)
	defer cancel()
	secret, err := c.Secrets.GetSecretValue(bounded, &secretsmanager.GetSecretValueInput{SecretId: aws.String(s.SecretARN)})
	if err != nil {
		return nil, err
	}
	if secret == nil || secret.SecretString == nil {
		return nil, v.ErrInvalid
	}
	hmac, encryption, err := decodeKeys(*secret.SecretString)
	if err != nil {
		return nil, err
	}
	protector, err := v.NewAESProtector(encryption)
	if err != nil {
		return nil, err
	}
	storage := store.Store{Client: c.Dynamo, Table: s.Table}
	service, err := v.NewService(storage, s.Policy, time.Now, hmac)
	if err != nil {
		return nil, err
	}
	return &Runtime{Service: service, Protector: protector, Store: storage, policy: s.Policy}, nil
}
func NewAWS(ctx context.Context, s Settings, c aws.Config) (*Runtime, error) {
	return New(ctx, s, Clients{Dynamo: ddb.NewFromConfig(c), Secrets: secretsmanager.NewFromConfig(c)})
}
func decodeKeys(raw string) ([]byte, []byte, error) {
	var keys struct {
		HMAC       string `json:"hmac"`
		Encryption string `json:"encryption"`
	}
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, nil, v.ErrInvalid
	}
	h, err := base64.StdEncoding.DecodeString(keys.HMAC)
	if err != nil || len(h) != 32 {
		return nil, nil, v.ErrInvalid
	}
	e, err := base64.StdEncoding.DecodeString(keys.Encryption)
	if err != nil || len(e) != 32 || subtle.ConstantTimeCompare(h, e) == 1 {
		return nil, nil, v.ErrInvalid
	}
	return h, e, nil
}

type WorkerSettings struct {
	HostID, Issuer, KeyARN, KeyID, Sender string
	AllowedRecipients                     []string
	Render                                sesmailer.Renderer
	Deliver                               worker.Delivery
	// Optional durable source. The default is a bounded scan for small workloads.
	Source   worker.Source
	MaxPages int
}

func (r *Runtime) NewWorker(c aws.Config, s WorkerSettings) (*worker.Worker, error) {
	if s.KeyARN == "" || s.KeyID == "" || s.Sender == "" {
		return nil, v.ErrInvalid
	}
	source := s.Source
	if source == nil {
		source = store.Scanner{Client: ddb.NewFromConfig(c), Table: r.Store.Table}
	}
	return worker.New(worker.Settings{Service: r.Service, Protector: r.Protector,
		Mailer: sesmailer.Mailer{Client: sesv2.NewFromConfig(c), Sender: s.Sender, AllowedRecipients: s.AllowedRecipients, Render: s.Render},
		Signer: kmssigner.Signer{Client: kms.NewFromConfig(c), ARN: s.KeyARN, ID: s.KeyID}, Deliver: s.Deliver,
		Source: source, CursorStore: r.Store, HostID: s.HostID, Issuer: s.Issuer, Policy: r.policy, MaxPages: s.MaxPages})
}
