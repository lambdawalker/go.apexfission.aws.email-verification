package infrastructure

import (
	"fmt"
	"sync"
	"testing"

	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/feature"
	composer "github.com/lambdawalker/go.infra.composer"
	adapter "github.com/lambdawalker/go.infra.composer/adapters/pulumi"
	"github.com/lambdawalker/go.infra.composer/ownership"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type monitor struct {
	mu    sync.Mutex
	calls []pulumi.MockResourceArgs
}

func (m *monitor) Call(a pulumi.MockCallArgs) (resource.PropertyMap, error) { return a.Args, nil }
func (m *monitor) NewResource(a pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, a)
	out := a.Inputs.Copy()
	out["name"] = resource.NewStringProperty(a.Name)
	out["arn"] = resource.NewStringProperty("arn:aws:mock::123:" + a.Name)
	out["keyId"] = resource.NewStringProperty(a.Name)
	return a.Name + "-id", out, nil
}
func metadata() ownership.Metadata {
	return ownership.Metadata{Application: "backend", Environment: "test", Domain: "example.test", Feature: "verification", Deployment: "backend-test", Owner: "email-verification"}
}
func TestComposedNativeResources(t *testing.T) {
	v, _ := feature.Declare("verification", config.Defaults())
	host := &composer.Feature{ID: "host", Requirements: []composer.Requirement{{ID: "verification", Contract: feature.Contract()}}, Resources: []composer.Resource{{ID: "runtime", Kind: "test:host", Inputs: map[string]composer.Value{"table": composer.RequirementRef("host", "verification", "table_name"), "key": composer.RequirementRef("host", "verification", "key_arn")}}}}
	app := composer.NewApplication("backend", host, v)
	if e := app.Inject("host", "verification", "verification", "infrastructure"); e != nil {
		t.Fatal(e)
	}
	plan, e := app.Plan(composer.Environment{Name: "test"})
	if e != nil {
		t.Fatal(e)
	}
	m := &monitor{}
	e = pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, e := Deploy(ctx, plan, metadata(), adapter.Registry{"test:host": func(ctx *pulumi.Context, name string, in pulumi.Map, opts ...pulumi.ResourceOption) (adapter.Registered, error) {
			r := &pulumi.CustomResourceState{}
			e := ctx.RegisterResource("test:index:Host", name, in, r, opts...)
			return adapter.Registered{Resource: r}, e
		}})
		return e
	}, pulumi.WithMocks("backend", "test", m))
	if e != nil {
		t.Fatal(e)
	}
	tables, keys, hosts := 0, 0, 0
	for _, c := range m.calls {
		switch c.TypeToken {
		case "aws:dynamodb/table:Table":
			tables++
			if c.Inputs["hashKey"].StringValue() != "pk" || c.Inputs["billingMode"].StringValue() != "PAY_PER_REQUEST" {
				t.Fatal("wrong store schema")
			}
			if _, ok := c.Inputs["ttl"]; ok {
				t.Fatal("unsafe automatic TTL")
			}
			if c.Inputs["tags"].ObjectValue()["ic:feature"].StringValue() != "verification" {
				t.Fatal("missing ownership")
			}
		case "aws:kms/key:Key":
			keys++
			if c.Inputs["keyUsage"].StringValue() != "SIGN_VERIFY" || c.Inputs["customerMasterKeySpec"].StringValue() != "ECC_NIST_P256" {
				t.Fatal("wrong signing key")
			}
		case "test:index:Host":
			hosts++
			if c.Inputs["table"].StringValue() != "verification.backend-transactions" || c.Inputs["key"].StringValue() != "arn:aws:mock::123:verification.backend-signing" {
				t.Fatal("native outputs not bound")
			}
		}
	}
	if tables != 1 || keys != 1 || hosts != 1 {
		t.Fatalf("unexpected resources %d %d %d", tables, keys, hosts)
	}
}
func TestUnsafePolicyRegistersNothing(t *testing.T) {
	v, _ := feature.Declare("verification", config.Defaults())
	v.Resources[0].Inputs["policy_json"] = composer.Literal(`{"retention_seconds":1}`)
	p, e := composer.NewApplication("backend", v).Plan(composer.Environment{Name: "test"})
	if e != nil {
		t.Fatal(e)
	}
	m := &monitor{}
	e = pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, e := Deploy(ctx, p, metadata(), nil)
		if e == nil {
			return fmt.Errorf("unsafe policy accepted")
		}
		return nil
	}, pulumi.WithMocks("backend", "test", m))
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range m.calls {
		if c.TypeToken != "pulumi:pulumi:Stack" {
			t.Fatalf("registered %s before validation", c.TypeToken)
		}
	}
}
