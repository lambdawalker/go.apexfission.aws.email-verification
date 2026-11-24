package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/lambdawalker/go.infra.composer/adapters/awsdiscovery"
	"github.com/lambdawalker/go.infra.composer/adapters/pulumi/awslabels"
	"github.com/lambdawalker/go.infra.composer/ownership"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() { pulumi.Run(deploy) }

func deploy(ctx *pulumi.Context) error {
	if ctx.Stack() != "shared" {
		return errors.New("deploy the discovery directory in the shared stack")
	}
	cfg := config.New(ctx, "")
	ownershipDomain := cfg.Require("domain")
	archive := cfg.Require("archive")
	info, err := os.Stat(archive)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("build the discovery Lambda archive first")
	}
	ctx, labels, err := awslabels.Install(ctx, ownership.Metadata{
		Application: ctx.Project(), Environment: "shared", Domain: ownershipDomain,
		Feature: "environment-discovery", Deployment: ctx.Project() + "-shared", Owner: "email-verification-testhost",
	})
	if err != nil {
		return err
	}
	host := fmt.Sprintf("index.components.email-verification.%s", ownershipDomain)
	index, err := awsdiscovery.Deploy(ctx, "demo-index", awsdiscovery.Settings{
		Domain: host, DNSProvider: cfg.Require("dnsProvider"), DNSZoneID: cfg.Require("dnsZoneId"),
		Code: pulumi.NewFileArchive(archive),
	}, awslabels.Option(ctx))
	if err != nil {
		return err
	}
	ctx.Export("discoveryURL", index.URL)
	ctx.Export("discoveryExecutionArn", index.ExecutionARN)
	ctx.Export("discoveryRegion", pulumi.String(config.New(ctx, "aws").Require("region")))
	return labels.Export(ctx, "resourceInventory")
}
