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
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/secretsmanager"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ses"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
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
		hmac, e := random.NewRandomBytes(ctx, name+"-hmac", &random.RandomBytesArgs{Length: pulumi.Int(32)}, pulumi.Parent(component))
		if e != nil {
			return adapter.Registered{}, e
		}
		encryption, e := random.NewRandomBytes(ctx, name+"-encryption", &random.RandomBytesArgs{Length: pulumi.Int(32)}, pulumi.Parent(component))
		if e != nil {
			return adapter.Registered{}, e
		}
		secret, e := awsnames.NewAutoNamed(ctx, names.SecretsManager, secretsmanager.NewSecret, name+"-secrets", &secretsmanager.SecretArgs{RecoveryWindowInDays: pulumi.Int(7)}, append(child, pulumi.Protect(true))...)
		if e != nil {
			return adapter.Registered{}, e
		}
		secretJSON := pulumi.All(hmac.Base64, encryption.Base64).ApplyT(func(values []any) (string, error) {
			raw, e := json.Marshal(map[string]string{"hmac": values[0].(string), "encryption": values[1].(string)})
			return string(raw), e
		}).(pulumi.StringOutput)
		version, e := secretsmanager.NewSecretVersion(ctx, name+"-secret-values", &secretsmanager.SecretVersionArgs{SecretId: secret.ID(), SecretString: pulumi.ToSecret(secretJSON).(pulumi.StringOutput)}, child...)
		if e != nil {
			return adapter.Registered{}, e
		}
		identity, e := awsnames.Input(names.SESEmailIdentity, pulumi.String(s.Sender))
		if e != nil {
			return adapter.Registered{}, e
		}
		sender, e := ses.NewEmailIdentity(ctx, name+"-sender", &ses.EmailIdentityArgs{Email: identity}, child...)
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
		policy := pulumi.All(table.Arn, in["table_arn"], in["key_arn"], secret.Arn, sender.Arn, logs.Arn).ApplyT(func(x []any) (string, error) {
			statement := func(actions []string, resources []any) map[string]any {
				return map[string]any{"Effect": "Allow", "Action": actions, "Resource": resources}
			}
			statements := []any{
				statement([]string{"dynamodb:GetItem", "dynamodb:PutItem"}, []any{x[0], x[1]}),
				statement([]string{"dynamodb:Scan"}, []any{x[1]}),
				statement([]string{"kms:Sign", "kms:GetPublicKey", "kms:DescribeKey"}, []any{x[2]}),
				statement([]string{"secretsmanager:GetSecretValue"}, []any{x[3]}),
				map[string]any{"Effect": "Allow", "Action": []string{"ses:SendEmail"}, "Resource": []any{x[4]}, "Condition": map[string]any{"StringEquals": map[string]any{"ses:FromAddress": s.Sender}, "ForAllValues:StringEquals": map[string]any{"ses:Recipients": []string{s.Recipient}}}},
				statement([]string{"logs:CreateLogStream", "logs:PutLogEvents"}, []any{fmt.Sprint(x[5]) + ":*"}),
			}
			raw, e := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": statements})
			return string(raw), e
		}).(pulumi.StringOutput)
		permissions, e := awsnames.NewAutoNamed(ctx, names.IAMInlinePolicy, iam.NewRolePolicy, name+"-permissions", &iam.RolePolicyArgs{Role: role.ID(), Policy: policy}, child...)
		if e != nil {
			return adapter.Registered{}, e
		}
		// Only ARNs/configuration go into the environment; secret bytes stay in SM.
		variables := pulumi.StringMap{"HOST_TABLE": table.Name, "VERIFIER_TABLE": in["table_name"].(pulumi.StringOutput), "KEY_ARN": in["key_arn"].(pulumi.StringOutput), "KEY_ID": in["key_id"].(pulumi.StringOutput), "EMAIL_VERIFICATION_POLICY": in["policy_json"].(pulumi.StringOutput), "SECRET_ARN": secret.Arn, "SENDER": pulumi.String(s.Sender), "RECIPIENT": pulumi.String(s.Recipient), "HOST_ID": pulumi.String(ctx.Project() + "-" + ctx.Stack())}
		function, e := lambda.NewFunction(ctx, functionName, &lambda.FunctionArgs{
			Name:          physicalName,
			Role:          role.Arn,
			Runtime:       pulumi.String("provided.al2023"),
			Handler:       pulumi.String("bootstrap"),
			Architectures: pulumi.StringArray{pulumi.String("arm64")},
			MemorySize:    pulumi.Int(256),
			Timeout:       pulumi.Int(120),
			Code:          pulumi.NewFileArchive(s.Archive),
			Environment:   &lambda.FunctionEnvironmentArgs{Variables: variables},
			LoggingConfig: &lambda.FunctionLoggingConfigArgs{LogFormat: pulumi.String("Text"), LogGroup: logs.Name},
		}, append(child,
			pulumi.Aliases([]pulumi.Alias{{Name: pulumi.String(previousName)}}),
			pulumi.DependsOn([]pulumi.Resource{permissions, version}),
		)...)
		if e != nil {
			return adapter.Registered{}, e
		}
		schedule, e := awsnames.NewAutoNamed(ctx, names.EventBridgeRule, cloudwatch.NewEventRule, name+"-worker", &cloudwatch.EventRuleArgs{ScheduleExpression: pulumi.String("rate(1 minute)")}, child...)
		if e != nil {
			return adapter.Registered{}, e
		}
		invoke, e := newLambdaPermission(ctx, name+"-worker-permission", &lambda.PermissionArgs{Action: pulumi.String("lambda:InvokeFunction"), Function: function.Name, Principal: pulumi.String("events.amazonaws.com"), SourceArn: schedule.Arn}, child...)
		if e != nil {
			return adapter.Registered{}, e
		}
		_, e = awsnames.NewAutoNamed(ctx, names.EventBridgeTarget, cloudwatch.NewEventTarget, name+"-worker-target", &cloudwatch.EventTargetArgs{Rule: schedule.Name, Arn: function.Arn, Input: pulumi.String(`{"action":"tick"}`), RetryPolicy: &cloudwatch.EventTargetRetryPolicyArgs{MaximumRetryAttempts: pulumi.Int(2), MaximumEventAgeInSeconds: pulumi.Int(120)}}, append(child, pulumi.DependsOn([]pulumi.Resource{invoke}))...)
		if e != nil {
			return adapter.Registered{}, e
		}
		if mobileHost != "" {
			if err := deployMobile(ctx, name, s.Mobile, mobileHost, function, child); err != nil {
				return adapter.Registered{}, err
			}
		}
		ctx.Export("testHostFunction", function.Name)
		ctx.Export("testHostFunctionArn", function.Arn)
		ctx.Export("testHostTable", table.Name)
		ctx.Export("testHostLogs", logs.Name)
		ctx.Export("senderVerificationRequired", pulumi.String(s.Sender))
		return adapter.Registered{Resource: component}, nil
	}
}
