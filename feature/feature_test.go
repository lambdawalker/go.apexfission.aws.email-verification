package feature_test

import (
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/feature"
	composer "github.com/lambdawalker/go.infra.composer"
	"testing"
)

func TestCompositionAndIsolation(t *testing.T) {
	first, e := feature.Declare("first", config.Defaults())
	if e != nil {
		t.Fatal(e)
	}
	second, e := feature.Declare("second", config.Defaults())
	if e != nil {
		t.Fatal(e)
	}
	host := &composer.Feature{ID: "host", Requirements: []composer.Requirement{{ID: "verification", Contract: feature.Contract()}}}
	app := composer.NewApplication("backend", host, first, second)
	if _, e = app.Plan(composer.Environment{Name: "prod"}); e == nil {
		t.Fatal("ambiguous providers accepted")
	}
	if e = app.Inject("host", "verification", "first", "infrastructure"); e != nil {
		t.Fatal(e)
	}
	p, e := app.Plan(composer.Environment{Name: "prod"})
	if e != nil {
		t.Fatal(e)
	}
	if e = feature.ValidatePlan(p); e != nil {
		t.Fatal(e)
	}
	if len(p.Resources()) != 2 || len(p.Bindings()) != 1 {
		t.Fatal("unexpected ownership or binding")
	}
	if _, e = feature.Configuration("prod", "host", "host-role", "deployment-role", config.Defaults()); e != nil {
		t.Fatal(e)
	}
}
func TestMissingAndIncompatibleProviders(t *testing.T) {
	for _, incompatible := range []bool{false, true} {
		host := &composer.Feature{ID: "host", Requirements: []composer.Requirement{{ID: "verification", Contract: feature.Contract()}}}
		fs := []*composer.Feature{host}
		if incompatible {
			v, _ := feature.Declare("verification", config.Defaults())
			v.Offerings[0].Contract.Major = 2
			fs = append(fs, v)
		}
		if _, e := composer.NewApplication("backend", fs...).Plan(composer.Environment{Name: "prod"}); e == nil {
			t.Fatal("invalid provider accepted")
		}
	}
}
func TestUnsafeTimingAndMutatedDeclaration(t *testing.T) {
	c := config.Defaults()
	c.RetentionSeconds = 1
	if _, e := feature.Declare("verification", c); e == nil {
		t.Fatal("unsafe retention accepted")
	}
	f, _ := feature.Declare("verification", config.Defaults())
	f.Resources[0].Inputs["policy_json"] = composer.Literal(`{"retention_seconds":1}`)
	p, e := composer.NewApplication("backend", f).Plan(composer.Environment{Name: "dev"})
	if e != nil {
		t.Fatal(e)
	}
	if e = feature.ValidatePlan(p); e == nil {
		t.Fatal("mutated unsafe policy accepted")
	}
}
