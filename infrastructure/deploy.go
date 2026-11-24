// Package infrastructure embeds verifier provisioning in a parent Pulumi program.
package infrastructure

import (
	"fmt"
	"strings"

	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/feature"
	composer "github.com/lambdawalker/go.infra.composer"
	adapter "github.com/lambdawalker/go.infra.composer/adapters/pulumi"
	"github.com/lambdawalker/go.infra.composer/adapters/pulumi/awslabels"
	"github.com/lambdawalker/go.infra.composer/adapters/pulumi/awsnames"
	names "github.com/lambdawalker/go.infra.composer/awsnames"
	"github.com/lambdawalker/go.infra.composer/ownership"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/dynamodb"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/kms"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Deploy validates all verifier policies before ANY factory runs. Extra factories
// belong to the parent application; overriding the verifier kind is forbidden.
// The parent selects the AWS provider/region/account through Pulumi options.
func Deploy(ctx *pulumi.Context, plan *composer.Plan, metadata ownership.Metadata, extra adapter.Registry, opts ...pulumi.ResourceOption) (*adapter.Result, error) {
	if err := feature.ValidatePlan(plan); err != nil {
		return nil, err
	}
	if metadata.Application != plan.Name() || metadata.Environment != plan.Environment().Name {
		return nil, fmt.Errorf("ownership must match application plan")
	}
	scoped, tracker, err := awslabels.Install(ctx, metadata)
	if err != nil {
		return nil, err
	}
	registry := adapter.Registry{}
	for kind, factory := range extra {
		if kind == feature.Kind {
			return nil, fmt.Errorf("verifier factory cannot be overridden")
		}
		registry[kind] = factory
	}
	// Capture validated literal policies; factories never parse deferred inputs.
	policies := map[string]string{}
	for _, r := range plan.Resources() {
		if r.Kind == feature.Kind {
			policies[r.ID] = r.Inputs["policy_json"].Node().Literal.(string)
		}
	}
	registry[feature.Kind] = func(ctx *pulumi.Context, name string, _ pulumi.Map, supplied ...pulumi.ResourceOption) (adapter.Registered, error) {
		raw, ok := policies[name]
		if !ok {
			return adapter.Registered{}, fmt.Errorf("unvalidated verifier resource")
		}
		policy, err := config.Load(strings.NewReader(raw))
		if err != nil {
			return adapter.Registered{}, err
		}
		scope, err := awslabels.ForFeature(ctx, strings.SplitN(name, ".", 2)[0])
		if err != nil {
			return adapter.Registered{}, err
		}
		options := append(append([]pulumi.ResourceOption{}, opts...), supplied...)
		component := &pulumi.ResourceState{}
		if err = scope.RegisterComponentResource(feature.Kind, name, component, options...); err != nil {
			return adapter.Registered{}, err
		}
		children := []pulumi.ResourceOption{pulumi.Parent(component), awslabels.Option(scope), pulumi.Protect(true)}
		table, err := awsnames.NewAutoNamed(scope, names.DynamoDBTable, dynamodb.NewTable, name+"-transactions", &dynamodb.TableArgs{
			BillingMode: pulumi.String("PAY_PER_REQUEST"), HashKey: pulumi.String("pk"),
			Attributes:           dynamodb.TableAttributeArray{&dynamodb.TableAttributeArgs{Name: pulumi.String("pk"), Type: pulumi.String("S")}},
			PointInTimeRecovery:  &dynamodb.TablePointInTimeRecoveryArgs{Enabled: pulumi.Bool(true)},
			ServerSideEncryption: &dynamodb.TableServerSideEncryptionArgs{Enabled: pulumi.Bool(true)},
			// TTL is deliberately absent: unresolved outcomes must never expire blindly.
		}, children...)
		if err != nil {
			return adapter.Registered{}, err
		}
		key, err := kms.NewKey(scope, name+"-signing", &kms.KeyArgs{
			CustomerMasterKeySpec: pulumi.String("ECC_NIST_P256"), KeyUsage: pulumi.String("SIGN_VERIFY"),
			Description:          pulumi.String("Email verification ES256 signing"),
			DeletionWindowInDays: pulumi.Int(policy.DeletionWaitDays),
		}, children...)
		if err != nil {
			return adapter.Registered{}, err
		}
		outputs := pulumi.Map{"table_name": table.Name, "table_arn": table.Arn, "key_id": key.KeyId, "key_arn": key.Arn, "policy_json": pulumi.String(raw)}
		if err = scope.RegisterResourceOutputs(component, outputs); err != nil {
			return adapter.Registered{}, err
		}
		return adapter.Registered{Resource: component, Outputs: map[string]pulumi.Output{"table_name": table.Name, "table_arn": table.Arn, "key_id": key.KeyId, "key_arn": key.Arn, "policy_json": pulumi.String(raw).ToStringOutput()}}, nil
	}
	result, err := adapter.Execute(scoped, plan, registry)
	if err != nil {
		return nil, err
	}
	if err = tracker.Export(scoped, "verificationOwnership"); err != nil {
		return nil, err
	}
	return result, nil
}
