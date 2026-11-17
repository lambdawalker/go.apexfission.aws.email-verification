// This example validates composition only. It creates no AWS resources.
package main

import (
	"fmt"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/feature"
	composer "github.com/lambdawalker/go.infra.composer"
)

func main() {
	verifier, err := feature.Declare("verification", config.Defaults())
	if err != nil {
		panic(err)
	}
	host := &composer.Feature{ID: "host", Requirements: []composer.Requirement{{ID: "verification", Contract: feature.Contract()}}, Runtime: []composer.RuntimeUse{{Requirement: "verification", Reason: "Host configures the imported verifier from provisioned outputs"}}}
	app := composer.NewApplication("composed-backend", host, verifier)
	if err = app.Inject("host", "verification", "verification", "infrastructure"); err != nil {
		panic(err)
	}
	plan, err := app.Plan(composer.Environment{Name: "dev"})
	if err != nil {
		panic(err)
	}
	if err = feature.ValidatePlan(plan); err != nil {
		panic(err)
	}
	if _, err = feature.Configuration("dev", "host", "host-role", "deployment-role", config.Defaults()); err != nil {
		panic(err)
	}
	fmt.Println(plan)
}
