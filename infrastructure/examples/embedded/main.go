package main

import (
	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/feature"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/infrastructure"
	composer "github.com/lambdawalker/go.infra.composer"
	"github.com/lambdawalker/go.infra.composer/ownership"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	pconfig "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	"strings"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		settings := pconfig.New(ctx, "")
		policy, err := config.Load(strings.NewReader(settings.Require("policyJSON")))
		if err != nil {
			return err
		}
		verifier, err := feature.Declare("verification", policy)
		if err != nil {
			return err
		}
		app := composer.NewApplication(ctx.Project(), verifier)
		plan, err := app.Plan(composer.Environment{Name: ctx.Stack()})
		if err != nil {
			return err
		}
		_, err = infrastructure.Deploy(ctx, plan, ownership.Metadata{Application: ctx.Project(), Environment: ctx.Stack(), Domain: settings.Require("domain"), Feature: "verification", Deployment: ctx.Project() + "-" + ctx.Stack(), Owner: "email-verification"}, nil)
		return err
	})
}
