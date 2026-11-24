package teststack

import (
	"archive/zip"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type mocks struct {
	mu            sync.Mutex
	rows          []pulumi.MockResourceArgs
	zoneLookupErr error
}

func (m *mocks) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) {
	if a.Token == "aws:route53/getZone:getZone" {
		if m.zoneLookupErr != nil {
			return nil, m.zoneLookupErr
		}
		return resource.PropertyMap{"zoneId": resource.NewStringProperty("ZTESTHOST")}, nil
	}
	return a.Args, nil
}
func (m *mocks) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, a)
	out := a.Inputs.Copy()
	out["name"] = resource.NewStringProperty(a.Name)
	out["arn"] = resource.NewStringProperty("arn:aws:mock::123:" + a.Name)
	out["keyId"] = resource.NewStringProperty("key-id")
	switch a.TypeToken {
	case "aws:acm/certificate:Certificate":
		name := a.Inputs["domainName"].StringValue()
		out["domainValidationOptions"] = resource.NewArrayProperty([]resource.PropertyValue{resource.NewObjectProperty(resource.PropertyMap{
			"domainName": resource.NewStringProperty(name), "resourceRecordName": resource.NewStringProperty("_proof." + name),
			"resourceRecordType": resource.NewStringProperty("CNAME"), "resourceRecordValue": resource.NewStringProperty("_token.acm-validations.aws."),
		})})
	case "aws:route53/record:Record":
		out["fqdn"] = a.Inputs["name"]
	case "cloudflare:index/dnsRecord:DnsRecord":
		out["name"] = a.Inputs["name"]
	case "aws:cognito/userPool:UserPool":
		out["endpoint"] = resource.NewStringProperty("cognito-idp.us-east-2.amazonaws.com/demo-pool")
	case "aws:apigatewayv2/api:Api":
		out["executionArn"] = resource.NewStringProperty("arn:aws:execute-api:us-east-2:123456789012:demo")
	case "aws:apigatewayv2/domainName:DomainName":
		fields := a.Inputs["domainNameConfiguration"].ObjectValue().Copy()
		fields["targetDomainName"] = resource.NewStringProperty("d-demo.execute-api.us-east-2.amazonaws.com")
		out["domainNameConfiguration"] = resource.NewObjectProperty(fields)
	}
	if a.TypeToken == "random:index/randomBytes:RandomBytes" {
		out["base64"] = resource.NewStringProperty(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))))
	}
	return a.Name + "-id", out, nil
}
func archive(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.zip")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	w, _ := z.Create("bootstrap")
	w.Write([]byte("test"))
	z.Close()
	f.Close()
	return path
}
func TestIsolatedStackWiring(t *testing.T) {
	m := &mocks{}
	s := Settings{Domain: "example.test", Sender: "sender@example.test", Recipient: "tester@example.test", Archive: archive(t), Policy: config.Defaults()}
	e := pulumi.RunErr(func(ctx *pulumi.Context) error { return Deploy(ctx, s) }, pulumi.WithMocks("email-verification-testhost", "test-ci", m))
	if e != nil {
		t.Fatal(e)
	}
	tables, functions, schedules, signers, secrets, identities, permissions := 0, 0, 0, 0, 0, 0, 0
	for _, r := range m.rows {
		switch r.TypeToken {
		case "aws:dynamodb/table:Table":
			tables++
			if _, ok := r.Inputs["ttl"]; ok {
				t.Fatal("unsafe TTL")
			}
		case "aws:kms/key:Key":
			signers++
		case "aws:secretsmanager/secretVersion:SecretVersion":
			secrets++
			if !r.Inputs["secretString"].IsSecret() {
				t.Fatal("unprotected secret state")
			}
		case "aws:ses/emailIdentity:EmailIdentity":
			identities++
		case "aws:lambda/function:Function":
			functions++
			if r.Name != "testhost-app-lambda" {
				t.Fatalf("Lambda logical name must produce a valid AWS function name, got %q", r.Name)
			}
			foundAlias := false
			for _, alias := range r.RegisterRPC.GetAliases() {
				if alias.GetSpec().GetName() == "testhost.app-lambda" {
					foundAlias = true
				}
			}
			if !foundAlias {
				t.Fatal("Lambda registration must retain the former dotted name as an alias")
			}
			vars := r.Inputs["environment"].ObjectValue()["variables"].ObjectValue()
			if vars["HOST_TABLE"].StringValue() == vars["VERIFIER_TABLE"].StringValue() {
				t.Fatal("shared records")
			}
			if vars["KEY_ARN"].StringValue() != "arn:aws:mock::123:verification.backend-signing" {
				t.Fatal("wrong signing key binding")
			}
			if vars["SENDER"].StringValue() != s.Sender || vars["RECIPIENT"].StringValue() != s.Recipient {
				t.Fatal("wrong mail binding")
			}
			for name := range vars {
				if strings.Contains(strings.ToLower(string(name)), "hmac") || strings.Contains(strings.ToLower(string(name)), "encryption") {
					t.Fatal("raw secret in environment")
				}
			}
		case "aws:lambda/permission:Permission":
			permissions++
			assertLambdaPermission(t, r, "testhost-app-worker-permission", "events.amazonaws.com", "arn:aws:mock::123:testhost.app-worker")
		case "aws:cloudwatch/eventTarget:EventTarget":
			schedules++
			if r.Inputs["input"].StringValue() != `{"action":"tick"}` {
				t.Fatal("wrong worker event")
			}
		case "aws:iam/rolePolicy:RolePolicy":
			var doc struct {
				Statement []struct {
					Action    []string
					Resource  []string
					Condition map[string]any
				}
			}
			if e = json.Unmarshal([]byte(r.Inputs["policy"].StringValue()), &doc); e != nil {
				t.Fatal(e)
			}
			for _, stmt := range doc.Statement {
				for _, res := range stmt.Resource {
					if res == "*" {
						t.Fatal("unscoped runtime access")
					}
				}
				for _, action := range stmt.Action {
					if action == "ses:SendEmail" && len(stmt.Condition) == 0 {
						t.Fatal("unrestricted recipient")
					}
				}
			}
		case "aws:lambda/functionUrl:FunctionUrl", "aws:apigatewayv2/api:Api":
			t.Fatal("public test endpoint")
		}
	}
	if tables != 2 || functions != 1 || schedules != 1 || signers != 1 || secrets != 1 || identities != 1 || permissions != 1 {
		t.Fatalf("unexpected isolated resources: %d %d %d %d %d %d %d", tables, functions, schedules, signers, secrets, identities, permissions)
	}
}
func TestUnsafePolicyCannotProvision(t *testing.T) {
	m := &mocks{}
	c := config.Defaults()
	c.RetentionSeconds = 1
	e := pulumi.RunErr(func(ctx *pulumi.Context) error {
		return Deploy(ctx, Settings{Domain: "example.test", Sender: "sender@example.test", Recipient: "tester@example.test", Archive: archive(t), Policy: c})
	}, pulumi.WithMocks("email-verification-testhost", "test-ci", m))
	if e == nil {
		t.Fatal("unsafe policy accepted")
	}
	for _, r := range m.rows {
		if r.TypeToken != "pulumi:pulumi:Stack" {
			t.Fatal("resource registered before rejection")
		}
	}
}

func assertLambdaPermission(t *testing.T, r pulumi.MockResourceArgs, name, principal, sourceARN string) {
	t.Helper()
	if r.Name != name {
		t.Fatalf("Lambda permission logical name = %q, want %q", r.Name, name)
	}
	if r.Inputs["action"].StringValue() != "lambda:InvokeFunction" ||
		r.Inputs["function"].StringValue() != "testhost-app-lambda" ||
		r.Inputs["principal"].StringValue() != principal ||
		r.Inputs["sourceArn"].StringValue() != sourceARN {
		t.Fatalf("incorrect invocation scope for %q", r.Name)
	}
	previousName := strings.Replace(name, "testhost-app-", "testhost.app-", 1)
	for _, alias := range r.RegisterRPC.GetAliases() {
		if alias.GetSpec().GetName() == previousName {
			return
		}
	}
	t.Fatalf("Lambda permission %q lacks alias %q", r.Name, previousName)
}
