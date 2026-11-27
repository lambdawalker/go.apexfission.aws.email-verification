package infrastructure

import (
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"strings"
	"testing"
)

func runtimeArgs() RuntimeArgs {
	return RuntimeArgs{Backend: Backend{TableName: pulumi.String("transactions"), TableARN: pulumi.String("arn:table"), KeyARN: pulumi.String("arn:key"), KeyID: pulumi.String("id")}, Policy: config.Defaults(), Sender: "mail@example.com", WorkerCode: pulumi.NewAssetArchive(map[string]interface{}{"bootstrap": pulumi.NewStringAsset("binary")})}
}
func TestIndependentConsumerCanReuseIdentityAndSecret(t *testing.T) {
	m := &monitor{}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		a := runtimeArgs()
		a.SecretARN = pulumi.String("arn:existing-secret")
		a.SenderARN = pulumi.String("arn:existing-identity")
		first, err := NewRuntime(ctx, "first", a)
		if err != nil {
			return err
		}
		if _, err = first.GrantAPI(ctx, "api-grants", pulumi.String("existing-api-role")); err != nil {
			return err
		}
		_, err = NewRuntime(ctx, "second", a)
		return err
	}, pulumi.WithMocks("consumer", "test", m))
	if err != nil {
		t.Fatal(err)
	}
	functions := 0
	for _, r := range m.calls {
		switch r.TypeToken {
		case "aws:secretsmanager/secret:Secret", "aws:secretsmanager/secretVersion:SecretVersion", "aws:ses/emailIdentity:EmailIdentity", "random:index/randomBytes:RandomBytes":
			t.Fatal("recreated supplied identity or secret")
		case "aws:lambda/function:Function":
			functions++
		case "aws:iam/rolePolicy:RolePolicy":
			if r.Name == "api-grants" {
				policy := r.Inputs["policy"].StringValue()
				for _, forbidden := range []string{"kms:", "ses:", "dynamodb:Scan", "arn:existing-identity"} {
					if strings.Contains(policy, forbidden) {
						t.Fatal("worker capability leaked to API")
					}
				}
			}
		}
	}
	if functions != 2 {
		t.Fatalf("expected independent workers, got %d", functions)
	}
}
func TestRuntimeRejectsUnsafeSettingsBeforeResources(t *testing.T) {
	for _, kind := range []string{"policy", "sender", "override", "archive"} {
		t.Run(kind, func(t *testing.T) {
			a := runtimeArgs()
			switch kind {
			case "policy":
				a.Policy.RetentionSeconds = 1
			case "sender":
				a.Sender = "bad"
			case "override":
				a.WorkerEnvironment = pulumi.StringMap{"SECRET_ARN": pulumi.String("rogue")}
			case "archive":
				a.WorkerCode = nil
			}
			m := &monitor{}
			err := pulumi.RunErr(func(ctx *pulumi.Context) error { _, err := NewRuntime(ctx, "invalid", a); return err }, pulumi.WithMocks("consumer", "test", m))
			if err == nil {
				t.Fatal("accepted invalid settings")
			}
			for _, r := range m.calls {
				if r.TypeToken != "pulumi:pulumi:Stack" {
					t.Fatal("registered resources before validation")
				}
			}
		})
	}
}
