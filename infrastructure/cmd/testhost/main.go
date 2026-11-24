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
			Mobile: mobileSettings(c.Get),
		})
	})
}

func mobileSettings(get func(string) string) teststack.MobileSettings {
	s := teststack.MobileSettings{
		BaseDomain: get("mobileBaseDomain"),
		DNSZone:    get("mobileDNSZone"),
		DNSZoneID:  get("mobileDNSZoneID"),
	}
	s.DNSProvider = get("mobileDNSProviderOverride")
	if s.DNSProvider != "" {
		return s
	}

	selected := get("dnsprovider")
	legacy := get("mobileDNSProvider")
	// Older wizards wrote route53 even when the user selected Cloudflare.
	// Keep legacy provider choices that came with a separate DNS configuration.
	switch {
	case legacy == "":
		s.DNSProvider = selected
	case legacy == "route53" && selected != "" && s.BaseDomain == "" && s.DNSZone == "" && s.DNSZoneID == "":
		s.DNSProvider = selected
	default:
		s.DNSProvider = legacy
	}
	return s
}
