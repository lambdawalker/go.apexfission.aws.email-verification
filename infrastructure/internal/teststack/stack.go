// Package teststack provisions only the isolated repository test host.
package teststack

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"

	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/feature"
	infra "github.com/lambdawalker/go.apexfission.aws.email-verification/infrastructure"
	composer "github.com/lambdawalker/go.infra.composer"
	adapter "github.com/lambdawalker/go.infra.composer/adapters/pulumi"
	"github.com/lambdawalker/go.infra.composer/adapters/pulumi/awslabels"
	"github.com/lambdawalker/go.infra.composer/adapters/pulumi/awsnames"
	names "github.com/lambdawalker/go.infra.composer/awsnames"
	"github.com/lambdawalker/go.infra.composer/ownership"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/cloudwatch"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/dynamodb"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/lambda"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

const kind = "apexfission:email-verification:TestHost"

type Settings struct {
	Domain, Sender, Recipient, Archive string
	Policy                             config.Config
	Mobile                             MobileSettings
}

func (s Settings) Validate() error {
	if err := s.Policy.Validate(); err != nil {
		return err
	}
	if s.Policy.TimeoutSeconds > 30 {
		return fmt.Errorf("test host timeout must be at most 30 seconds")
	}
	for _, value := range []string{s.Sender, s.Recipient} {
		a, e := mail.ParseAddress(value)
		if e != nil || a.Address != value {
			return fmt.Errorf("sender and recipient must be plain email addresses")
		}
	}
	if s.Archive == "" {
		return fmt.Errorf("Lambda archive required")
	}
	archive, err := zip.OpenReader(s.Archive)
	if err != nil {
		return fmt.Errorf("readable Lambda ZIP required")
	}
	defer archive.Close()
	for _, entry := range archive.File {
		if entry.Name == "bootstrap" && entry.UncompressedSize64 > 0 {
			return nil
		}
	}
	return fmt.Errorf("Lambda ZIP must contain bootstrap")
}
func Deploy(ctx *pulumi.Context, s Settings) error {
	if !strings.HasPrefix(ctx.Stack(), "test-") {
		return fmt.Errorf("test host requires a dedicated test-* stack")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	s.Mobile = s.Mobile.withOwnershipDefaults(s.Domain)
	var mobileHost string
	if s.Mobile.BaseDomain != "" {
		var err error
		mobileHost, err = s.Mobile.hostname(ctx.Stack())
		if err != nil {
			return err
		}
		s.Mobile, err = s.Mobile.resolveDNSZone(ctx, mobileHost)
		if err != nil {
			return err
		}
	}
	// Exact scope is explicit in the program configuration; this is never the
	// parent application's default stack entry point.
	vf, e := feature.Declare("verification", s.Policy)
	if e != nil {
		return e
	}
	inputs := map[string]composer.Value{}
	for field := range feature.Contract().Fields {
		inputs[field] = composer.RequirementRef("testhost", "verification", field)
	}
	host := &composer.Feature{ID: "testhost", Requirements: []composer.Requirement{{ID: "verification", Contract: feature.Contract()}}, Resources: []composer.Resource{{ID: "app", Kind: kind, Inputs: inputs}}}
	app := composer.NewApplication(ctx.Project(), vf, host)
	if e = app.Inject("testhost", "verification", "verification", "infrastructure"); e != nil {
		return e
	}
	plan, e := app.Plan(composer.Environment{Name: ctx.Stack()})
	if e != nil {
		return e
	}
	if _, e = feature.Configuration(ctx.Stack(), "testhost", "testhost-role", "deployment-role", s.Policy); e != nil {
		return e
	}
	_, e = infra.Deploy(ctx, plan, ownership.Metadata{Application: ctx.Project(), Environment: ctx.Stack(), Domain: s.Domain, Feature: "verification", Deployment: ctx.Project() + "-" + ctx.Stack(), Owner: "email-verification-testhost"}, adapter.Registry{kind: factory(s, mobileHost)})
	return e
}
func factory(s Settings, mobileHost string) adapter.Factory {
	return func(ctx *pulumi.Context, name string, in pulumi.Map, opts ...pulumi.ResourceOption) (adapter.Registered, error) {
		// Pulumi uses the logical name as the base of the AWS function name.
		// Lambda disallows the dot in Composer's qualified resource name.
		previousName := name + "-lambda"
		functionName := strings.ReplaceAll(previousName, ".", "-")
		physicalName, e := awsnames.LambdaFunctionName(functionName, nil)
		if e != nil {
			return adapter.Registered{}, e
		}
		ctx, e = awslabels.ForFeature(ctx, "testhost")
		if e != nil {
			return adapter.Registered{}, e
		}
		component := &pulumi.ResourceState{}
		if e = ctx.RegisterComponentResource(kind, name, component, opts...); e != nil {
			return adapter.Registered{}, e
		}
		child := []pulumi.ResourceOption{pulumi.Parent(component), awslabels.Option(ctx)}
		table, e := awsnames.NewAutoNamed(ctx, names.DynamoDBTable, dynamodb.NewTable, name+"-records", &dynamodb.TableArgs{BillingMode: pulumi.String("PAY_PER_REQUEST"), HashKey: pulumi.String("pk"), Attributes: dynamodb.TableAttributeArray{&dynamodb.TableAttributeArgs{Name: pulumi.String("pk"), Type: pulumi.String("S")}}, PointInTimeRecovery: &dynamodb.TablePointInTimeRecoveryArgs{Enabled: pulumi.Bool(true)}, ServerSideEncryption: &dynamodb.TableServerSideEncryptionArgs{Enabled: pulumi.Bool(true)}}, append(child, pulumi.Protect(true))...)
		if e != nil {
			return adapter.Registered{}, e
		}
		role, e := awsnames.NewAutoNamed(ctx, names.IAMRole, iam.NewRole, name+"-role", &iam.RoleArgs{AssumeRolePolicy: pulumi.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`)}, child...)
		if e != nil {
			return adapter.Registered{}, e
		}
		// A fixed logical log group allows an exact log-write permission before Lambda.
		logs, e := awsnames.NewAutoNamed(ctx, names.CloudWatchLogGroup, cloudwatch.NewLogGroup, name+"-logs", &cloudwatch.LogGroupArgs{RetentionInDays: pulumi.Int(7)}, child...)
		if e != nil {
			return adapter.Registered{}, e
		}
		hostPolicy := table.Arn.ApplyT(func(arn string) (string, error) {
			raw, err := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Action": []string{"dynamodb:GetItem", "dynamodb:PutItem"}, "Resource": []string{arn}}}})
			return string(raw), err
		}).(pulumi.StringOutput)
		verificationScope, e := awslabels.ForFeature(ctx, "verification")
		if e != nil {
			return adapter.Registered{}, e
		}
		runtime, e := infra.NewRuntime(verificationScope, "verification.runtime", infra.RuntimeArgs{
			Backend: infra.Backend{TableName: in["table_name"].(pulumi.StringOutput), TableARN: in["table_arn"].(pulumi.StringOutput), KeyID: in["key_id"].(pulumi.StringOutput), KeyARN: in["key_arn"].(pulumi.StringOutput)},
			Policy:  s.Policy, Sender: s.Sender, AllowedRecipients: []string{s.Recipient}, WorkerCode: pulumi.NewFileArchive(s.Archive), HostPolicy: hostPolicy,
			WorkerEnvironment: pulumi.StringMap{"HOST_TABLE": table.Name, "HOST_ID": pulumi.String(ctx.Project() + "-" + ctx.Stack()), "RECIPIENT": pulumi.String(s.Recipient), "VERIFICATION_MODE": pulumi.String("worker")},
			Migration:         &infra.Migration{Parent: component, Prefix: name},
		}, awslabels.Option(verificationScope))
		if e != nil {
			return adapter.Registered{}, e
		}
		apiAccess, e := runtime.GrantAPI(ctx, name+"-verification-access", role.ID(), child...)
		if e != nil {
			return adapter.Registered{}, e
		}
		policy := pulumi.All(table.Arn, logs.Arn).ApplyT(func(x []any) (string, error) {
			raw, err := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": []any{
				map[string]any{"Effect": "Allow", "Action": []string{"dynamodb:GetItem", "dynamodb:PutItem"}, "Resource": []string{x[0].(string)}},
				map[string]any{"Effect": "Allow", "Action": []string{"logs:CreateLogStream", "logs:PutLogEvents"}, "Resource": []string{x[1].(string) + ":*"}},
			}})
			return string(raw), err
		}).(pulumi.StringOutput)
		permissions, e := awsnames.NewAutoNamed(ctx, names.IAMInlinePolicy, iam.NewRolePolicy, name+"-permissions", &iam.RolePolicyArgs{Role: role.ID(), Policy: policy}, child...)
		if e != nil {
			return adapter.Registered{}, e
		}
		// Only ARNs/configuration go into the environment; secret bytes stay in SM.
		variables := pulumi.StringMap{"HOST_TABLE": table.Name, "HOST_ID": pulumi.String(ctx.Project() + "-" + ctx.Stack()), "RECIPIENT": pulumi.String(s.Recipient), "VERIFICATION_MODE": pulumi.String("api")}
		for key, value := range runtime.APIEnvironment {
			variables[key] = value
		}
		function, e := lambda.NewFunction(ctx, functionName, &lambda.FunctionArgs{
			Name:          physicalName,
			Role:          role.Arn,
			Runtime:       pulumi.String("provided.al2023"),
			Handler:       pulumi.String("bootstrap"),
			Architectures: pulumi.StringArray{pulumi.String("arm64")},
			MemorySize:    pulumi.Int(256),
			Timeout:       pulumi.Int(30),
			Code:          pulumi.NewFileArchive(s.Archive),
			Environment:   &lambda.FunctionEnvironmentArgs{Variables: variables},
			LoggingConfig: &lambda.FunctionLoggingConfigArgs{LogFormat: pulumi.String("Text"), LogGroup: logs.Name},
		}, append(child,
			pulumi.Aliases([]pulumi.Alias{{Name: pulumi.String(previousName)}}),
			pulumi.DependsOn([]pulumi.Resource{permissions, apiAccess}),
		)...)
		if e != nil {
			return adapter.Registered{}, e
		}
		if mobileHost != "" {
			if err := deployMobile(ctx, name, s.Mobile, mobileHost, function, child); err != nil {
				return adapter.Registered{}, err
			}
		}
		ctx.Export("testHostWorkerFunction", runtime.Worker.Name)
		ctx.Export("testHostWorkerFunctionArn", runtime.Worker.Arn)
		ctx.Export("testHostWorkerLogs", runtime.Logs.Name)
		ctx.Export("testHostFunction", function.Name)
		ctx.Export("testHostFunctionArn", function.Arn)
		ctx.Export("testHostTable", table.Name)
		ctx.Export("testHostLogs", logs.Name)
		ctx.Export("senderVerificationRequired", pulumi.String(s.Sender))
		return adapter.Registered{Resource: component}, nil
	}
}
