package infrastructure

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"

	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.infra.composer/adapters/pulumi/awsnames"
	names "github.com/lambdawalker/go.infra.composer/awsnames"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/cloudwatch"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/lambda"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/secretsmanager"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ses"
	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Backend contains native outputs from feature.Contract().
type Backend struct{ TableName, TableARN, KeyID, KeyARN pulumi.StringInput }

// Migration adopts resources previously registered under another component.
// Aliases preserve their physical identities and the existing secret bytes.
type Migration struct {
	Parent pulumi.Resource
	Prefix string
}
type RuntimeArgs struct {
	Backend Backend
	Policy  config.Config
	Sender  string
	// Existing identity/secret references are never managed or deleted here.
	SenderARN         pulumi.StringInput
	SecretARN         pulumi.StringInput
	AllowedRecipients []string
	WorkerCode        pulumi.ArchiveInput
	// Host-owned configuration and permissions needed by its acceptance adapter.
	WorkerEnvironment pulumi.StringMap
	HostPolicy        pulumi.StringInput
	Migration         *Migration
}
type Runtime struct {
	pulumi.ResourceState
	SecretARN      pulumi.StringOutput
	SenderARN      pulumi.StringOutput
	Worker         *lambda.Function
	Role           *iam.Role
	Logs           *cloudwatch.LogGroup
	APIEnvironment pulumi.StringMap
	APIPolicy      pulumi.StringOutput
}

func (a RuntimeArgs) validate() error {
	if err := a.Policy.Validate(); err != nil {
		return err
	}
	if a.Migration != nil && (a.Migration.Parent == nil || a.Migration.Prefix == "") {
		return fmt.Errorf("migration requires the former parent and resource prefix")
	}
	if a.Policy.TimeoutSeconds > 30 {
		return fmt.Errorf("Lambda worker external-call timeout must be at most 30 seconds")
	}
	if a.Backend.TableName == nil || a.Backend.TableARN == nil || a.Backend.KeyID == nil || a.Backend.KeyARN == nil || a.WorkerCode == nil {
		return fmt.Errorf("backend outputs and worker archive required")
	}
	for _, address := range append([]string{a.Sender}, a.AllowedRecipients...) {
		p, err := mail.ParseAddress(address)
		if err != nil || p.Address != address {
			return fmt.Errorf("plain sender/recipient email address required")
		}
	}
	for _, key := range []string{"VERIFIER_TABLE", "SECRET_ARN", "EMAIL_VERIFICATION_POLICY", "KEY_ARN", "KEY_ID", "SENDER"} {
		if _, ok := a.WorkerEnvironment[key]; ok {
			return fmt.Errorf("worker environment cannot override %s", key)
		}
	}
	return nil
}

// NewRuntime adds email delivery and a worker to an existing storage/signing
// feature. The caller supplies its compiled acceptance adapter in WorkerCode.
// Call in the feature's ownership scope, with the parent's AWS provider options.
func NewRuntime(ctx *pulumi.Context, name string, a RuntimeArgs, opts ...pulumi.ResourceOption) (*Runtime, error) {
	if err := a.validate(); err != nil {
		return nil, err
	}
	logical := strings.ReplaceAll(name+"-worker-lambda", ".", "-")
	physical, err := awsnames.LambdaFunctionName(logical, nil)
	if err != nil {
		return nil, err
	}
	permissionName := strings.ReplaceAll(name+"-worker-permission", ".", "-")
	if err := awsnames.ValidateLambdaStatementID(permissionName); err != nil {
		return nil, err
	}
	r := &Runtime{}
	if err := ctx.RegisterComponentResource("apexfission:email-verification:Runtime", name, r, opts...); err != nil {
		return nil, err
	}
	children := []pulumi.ResourceOption{pulumi.Parent(r)}
	child := func(suffix string, protect bool) []pulumi.ResourceOption {
		o := append([]pulumi.ResourceOption{}, children...)
		if protect {
			o = append(o, pulumi.Protect(true))
		}
		if a.Migration != nil {
			o = append(o, pulumi.Aliases([]pulumi.Alias{{Name: pulumi.String(a.Migration.Prefix + suffix), Parent: a.Migration.Parent}}))
		}
		return o
	}
	if a.SecretARN != nil {
		r.SecretARN = a.SecretARN.ToStringOutput()
	} else {
		h, err := random.NewRandomBytes(ctx, name+"-hmac", &random.RandomBytesArgs{Length: pulumi.Int(32)}, child("-hmac", false)...)
		if err != nil {
			return nil, err
		}
		e, err := random.NewRandomBytes(ctx, name+"-encryption", &random.RandomBytesArgs{Length: pulumi.Int(32)}, child("-encryption", false)...)
		if err != nil {
			return nil, err
		}
		secret, err := awsnames.NewAutoNamed(ctx, names.SecretsManager, secretsmanager.NewSecret, name+"-secrets", &secretsmanager.SecretArgs{RecoveryWindowInDays: pulumi.Int(7)}, child("-secrets", true)...)
		if err != nil {
			return nil, err
		}
		payload := pulumi.All(h.Base64, e.Base64).ApplyT(func(x []any) (string, error) {
			b, err := json.Marshal(map[string]string{"hmac": x[0].(string), "encryption": x[1].(string)})
			return string(b), err
		}).(pulumi.StringOutput)
		version, err := secretsmanager.NewSecretVersion(ctx, name+"-secret-values", &secretsmanager.SecretVersionArgs{SecretId: secret.ID(), SecretString: pulumi.ToSecret(payload).(pulumi.StringOutput)}, child("-secret-values", false)...)
		if err != nil {
			return nil, err
		}
		// Both API and worker depend on the populated version, not only its container.
		r.SecretARN = pulumi.All(secret.Arn, version.ID()).ApplyT(func(x []any) string { return x[0].(string) }).(pulumi.StringOutput)
	}
	if a.SenderARN != nil {
		r.SenderARN = a.SenderARN.ToStringOutput()
	} else {
		sender, err := ses.NewEmailIdentity(ctx, name+"-sender", &ses.EmailIdentityArgs{Email: pulumi.String(a.Sender)}, child("-sender", false)...)
		if err != nil {
			return nil, err
		}
		r.SenderARN = sender.Arn
	}
	policyJSON, _ := json.Marshal(a.Policy)
	r.APIEnvironment = pulumi.StringMap{"VERIFIER_TABLE": a.Backend.TableName, "SECRET_ARN": r.SecretARN, "EMAIL_VERIFICATION_POLICY": pulumi.String(policyJSON)}
	r.APIPolicy = verificationPolicy(a.Backend, r.SecretARN, r.SenderARN, a.Sender, a.AllowedRecipients, false)
	role, err := awsnames.NewAutoNamed(ctx, names.IAMRole, iam.NewRole, name+"-worker-role", &iam.RoleArgs{AssumeRolePolicy: pulumi.String(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]}`)}, children...)
	if err != nil {
		return nil, err
	}
	r.Role = role
	logs, err := awsnames.NewAutoNamed(ctx, names.CloudWatchLogGroup, cloudwatch.NewLogGroup, name+"-worker-logs", &cloudwatch.LogGroupArgs{RetentionInDays: pulumi.Int(14)}, children...)
	if err != nil {
		return nil, err
	}
	r.Logs = logs
	access, err := awsnames.NewAutoNamed(ctx, names.IAMInlinePolicy, iam.NewRolePolicy, name+"-worker-access", &iam.RolePolicyArgs{Role: role.ID(), Policy: verificationPolicy(a.Backend, r.SecretARN, r.SenderARN, a.Sender, a.AllowedRecipients, true)}, children...)
	if err != nil {
		return nil, err
	}
	logPolicy, err := awsnames.NewAutoNamed(ctx, names.IAMInlinePolicy, iam.NewRolePolicy, name+"-worker-logging", &iam.RolePolicyArgs{Role: role.ID(), Policy: pulumi.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["logs:CreateLogStream","logs:PutLogEvents"],"Resource":[%q]}]}`, pulumi.Sprintf("%s:*", logs.Arn))}, children...)
	if err != nil {
		return nil, err
	}
	dependencies := []pulumi.Resource{access, logPolicy}
	if a.HostPolicy != nil {
		p, err := awsnames.NewAutoNamed(ctx, names.IAMInlinePolicy, iam.NewRolePolicy, name+"-host-acceptance", &iam.RolePolicyArgs{Role: role.ID(), Policy: a.HostPolicy}, children...)
		if err != nil {
			return nil, err
		}
		dependencies = append(dependencies, p)
	}
	environment := pulumi.StringMap{}
	for k, v := range r.APIEnvironment {
		environment[k] = v
	}
	for k, v := range a.WorkerEnvironment {
		environment[k] = v
	}
	environment["KEY_ARN"], environment["KEY_ID"], environment["SENDER"] = a.Backend.KeyARN, a.Backend.KeyID, pulumi.String(a.Sender)
	r.Worker, err = lambda.NewFunction(ctx, logical, &lambda.FunctionArgs{Name: physical, Role: role.Arn, Runtime: pulumi.String("provided.al2023"), Handler: pulumi.String("bootstrap"), Architectures: pulumi.StringArray{pulumi.String("arm64")}, MemorySize: pulumi.Int(256), Timeout: pulumi.Int(120), Code: a.WorkerCode, Environment: &lambda.FunctionEnvironmentArgs{Variables: environment}, LoggingConfig: &lambda.FunctionLoggingConfigArgs{LogFormat: pulumi.String("Text"), LogGroup: logs.Name}}, append(children, pulumi.DependsOn(dependencies))...)
	if err != nil {
		return nil, err
	}
	schedule, err := awsnames.NewAutoNamed(ctx, names.EventBridgeRule, cloudwatch.NewEventRule, name+"-worker", &cloudwatch.EventRuleArgs{ScheduleExpression: pulumi.String("rate(1 minute)")}, child("-worker", false)...)
	if err != nil {
		return nil, err
	}
	permissions := append([]pulumi.ResourceOption{}, children...)
	if a.Migration != nil {
		permissions = append(permissions, pulumi.Aliases([]pulumi.Alias{{Name: pulumi.String(strings.ReplaceAll(a.Migration.Prefix+"-worker-permission", ".", "-")), Parent: a.Migration.Parent}, {Name: pulumi.String(a.Migration.Prefix + "-worker-permission"), Parent: a.Migration.Parent}}))
	}
	permission, err := lambda.NewPermission(ctx, permissionName, &lambda.PermissionArgs{StatementId: pulumi.String(permissionName), Action: pulumi.String("lambda:InvokeFunction"), Function: r.Worker.Name, Principal: pulumi.String("events.amazonaws.com"), SourceArn: schedule.Arn}, permissions...)
	if err != nil {
		return nil, err
	}
	_, err = awsnames.NewAutoNamed(ctx, names.EventBridgeTarget, cloudwatch.NewEventTarget, name+"-worker-target", &cloudwatch.EventTargetArgs{Rule: schedule.Name, Arn: r.Worker.Arn, Input: pulumi.String(`{"action":"tick"}`), RetryPolicy: &cloudwatch.EventTargetRetryPolicyArgs{MaximumRetryAttempts: pulumi.Int(2), MaximumEventAgeInSeconds: pulumi.Int(120)}}, append(child("-worker-target", false), pulumi.DependsOn([]pulumi.Resource{permission}))...)
	if err != nil {
		return nil, err
	}
	if err = ctx.RegisterResourceOutputs(r, pulumi.Map{"secret_arn": r.SecretARN, "sender_arn": r.SenderARN, "worker_arn": r.Worker.Arn}); err != nil {
		return nil, err
	}
	return r, nil
}

func verificationPolicy(b Backend, secret, sender pulumi.StringInput, address string, recipients []string, worker bool) pulumi.StringOutput {
	return pulumi.All(b.TableARN, b.KeyARN, secret, sender).ApplyT(func(x []any) (string, error) {
		stmt := func(actions []string, resource any) map[string]any {
			return map[string]any{"Effect": "Allow", "Action": actions, "Resource": []any{resource}}
		}
		statements := []any{stmt([]string{"dynamodb:GetItem", "dynamodb:PutItem"}, x[0]), stmt([]string{"secretsmanager:GetSecretValue"}, x[2])}
		if worker {
			conditions := map[string]any{"StringEquals": map[string]any{"ses:FromAddress": address}}
			if len(recipients) > 0 {
				conditions["ForAllValues:StringEquals"] = map[string]any{"ses:Recipients": recipients}
			}
			mail := stmt([]string{"ses:SendEmail"}, x[3])
			mail["Condition"] = conditions
			statements = append(statements, stmt([]string{"dynamodb:Scan"}, x[0]), stmt([]string{"kms:Sign", "kms:GetPublicKey", "kms:DescribeKey"}, x[1]), mail)
		}
		raw, err := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": statements})
		return string(raw), err
	}).(pulumi.StringOutput)
}

// GrantAPI attaches only the verification permissions needed by a host API.
// Host storage and logging grants remain the application's responsibility.
func (r *Runtime) GrantAPI(ctx *pulumi.Context, name string, role pulumi.StringInput, opts ...pulumi.ResourceOption) (*iam.RolePolicy, error) {
	return awsnames.NewAutoNamed(ctx, names.IAMInlinePolicy, iam.NewRolePolicy, name, &iam.RolePolicyArgs{Role: role, Policy: r.APIPolicy}, opts...)
}
