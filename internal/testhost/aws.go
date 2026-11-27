package testhost

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	v "github.com/lambdawalker/go.apexfission.aws.email-verification"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/adapters/awsruntime"
	store "github.com/lambdawalker/go.apexfission.aws.email-verification/adapters/dynamodb"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/adapters/kmstrust"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/worker"
)

// Only the demo selects its host table, fixed recipient, identity and trust policy.
func initialize(ctx context.Context) (*App, *awsruntime.Runtime, aws.Config, error) {
	s, err := awsruntime.FromEnvironment(os.Getenv)
	if err != nil {
		return nil, nil, aws.Config{}, err
	}
	hostTable, hostID, recipient := os.Getenv("HOST_TABLE"), os.Getenv("HOST_ID"), os.Getenv("RECIPIENT")
	if hostTable == "" || hostTable == s.Table || hostID == "" || recipient == "" || s.Policy.TimeoutSeconds > 30 {
		return nil, nil, aws.Config{}, v.ErrInvalid
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, nil, cfg, err
	}
	runtime, err := awsruntime.NewAWS(ctx, s, cfg)
	if err != nil {
		return nil, nil, cfg, err
	}
	hostStore := store.Store{Client: dynamodb.NewFromConfig(cfg), Table: hostTable}
	host, err := v.NewHost(hostStore, s.Policy, time.Now)
	if err != nil {
		return nil, nil, cfg, err
	}
	app := &App{Host: host, Service: runtime.Service, Store: hostStore, Protector: runtime.Protector, Policy: s.Policy, HostID: hostID, Recipient: recipient, Now: time.Now}
	return app, runtime, cfg, nil
}
func FromEnvironment(ctx context.Context) (*App, error) {
	app, _, _, err := initialize(ctx)
	return app, err
}
func WorkerFromEnvironment(ctx context.Context) (*worker.Worker, error) {
	app, runtime, cfg, err := initialize(ctx)
	if err != nil {
		return nil, err
	}
	arn, id, sender := os.Getenv("KEY_ARN"), os.Getenv("KEY_ID"), os.Getenv("SENDER")
	if arn == "" || id == "" || sender == "" {
		return nil, errors.New("missing worker configuration")
	}
	issuer := "urn:email-verification:test:" + app.HostID
	trust := func(ctx context.Context) (v.Trust, error) {
		return kmstrust.Load(ctx, kms.NewFromConfig(cfg), arn, issuer, app.Policy)
	}
	return runtime.NewWorker(cfg, awsruntime.WorkerSettings{HostID: app.HostID, Issuer: issuer, KeyARN: arn, KeyID: id, Sender: sender, AllowedRecipients: []string{app.Recipient}, Deliver: worker.InProcess(app.Service, app.Host, issuer, trust)})
}
