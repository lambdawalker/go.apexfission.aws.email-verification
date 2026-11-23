package main

import (
	"strings"

	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/lambdawalker/go.apexfission.aws.email-verification/infrastructure/internal/teststack"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	pc "github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		c := pc.New(ctx, "")
		policy, e := config.Load(strings.NewReader(c.Require("policyJSON")))
		if e != nil {
			return e
		}
		return teststack.Deploy(ctx, teststack.Settings{
			Domain: c.Require("domain"), Sender: c.Require("sender"), Recipient: c.Require("recipient"),
			Archive: c.Require("archive"), Policy: policy,
			Mobile: teststack.MobileSettings{
				BaseDomain: c.Get("mobileBaseDomain"), DNSProvider: c.Get("mobileDNSProvider"),
				DNSZone: c.Get("mobileDNSZone"), DNSZoneID: c.Get("mobileDNSZoneID"),
			},
		})
	})
}
