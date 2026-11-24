package teststack

import (
	"errors"
	"strings"
	"testing"

	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func TestMobileDemoDomainAndAuthorization(t *testing.T) {
	for _, provider := range []string{"route53", "cloudflare"} {
		t.Run(provider, func(t *testing.T) {
			m := &mocks{}
			dns := MobileSettings{BaseDomain: "components.email-verification.apexfission.com",
				DNSZone: "apexfission.com", DNSProvider: provider}
			s := Settings{Domain: "tags.example.test", Sender: "sender@example.test", Recipient: "tester@example.test",
				Archive: archive(t), Policy: config.Defaults(), Mobile: dns}
			err := pulumi.RunErr(func(ctx *pulumi.Context) error { return Deploy(ctx, s) }, pulumi.WithMocks("email-verification-testhost", "test-qa", m))
			if err != nil {
				t.Fatal(err)
			}
			if provider == "cloudflare" {
				for _, invoke := range m.invocations {
					if invoke.Token == "aws:route53/getZone:getZone" {
						t.Fatal("Cloudflare selection attempted a Route 53 lookup")
					}
					if invoke.Token == "cloudflare:index/getZones:getZones" &&
						(invoke.Args["name"].StringValue() != dns.DNSZone || invoke.Args["status"].StringValue() != "active") {
						t.Fatal("unexpected Cloudflare zone filters")
					}
				}
			}
			host := "qa.components.email-verification.apexfission.com"
			routes, records, mappings, authorizers := 0, 0, 0, 0
			permissions := map[string]string{
				"testhost-app-demo-permission-start":   "start",
				"testhost-app-demo-permission-confirm": "confirm",
				"testhost-app-demo-permission-status":  "status",
			}
			for _, r := range m.rows {
				switch r.TypeToken {
				case "aws:apigatewayv2/api:Api":
					if !r.Inputs["disableExecuteApiEndpoint"].BoolValue() {
						t.Fatal("default API hostname was left enabled")
					}
				case "aws:apigatewayv2/authorizer:Authorizer":
					authorizers++
					if r.Inputs["authorizerType"].StringValue() != "JWT" {
						t.Fatal("not a JWT authorizer")
					}
				case "aws:apigatewayv2/route:Route":
					routes++
					if r.Inputs["authorizationType"].StringValue() != "JWT" || !strings.HasPrefix(r.Inputs["routeKey"].StringValue(), "POST /v1/verification/") {
						t.Fatal("unexpected or unprotected route")
					}
				case "aws:lambda/permission:Permission":
					if r.Inputs["principal"].StringValue() != "apigateway.amazonaws.com" {
						continue
					}
					action, ok := permissions[r.Name]
					if !ok {
						t.Fatalf("unexpected API Gateway Lambda permission %q", r.Name)
					}
					assertLambdaPermission(t, r, r.Name, "apigateway.amazonaws.com", "arn:aws:execute-api:us-east-2:123456789012:demo/$default/POST/v1/verification/"+action)
					delete(permissions, r.Name)
				case "aws:apigatewayv2/apiMapping:ApiMapping":
					mappings++
				case "aws:apigatewayv2/domainName:DomainName":
					if r.Inputs["domainName"].StringValue() != host {
						t.Fatal("hostname did not use the environment")
					}
				case "aws:route53/record:Record", "cloudflare:index/dnsRecord:DnsRecord":
					if r.TypeToken == "aws:route53/record:Record" && r.Inputs["zoneId"].StringValue() != "ZTESTHOST" {
						t.Fatal("record did not use the public zone resolved before registration")
					}
					if provider == "cloudflare" && (r.TypeToken != "cloudflare:index/dnsRecord:DnsRecord" ||
						r.Inputs["zoneId"].StringValue() != strings.Repeat("a", 32) || r.Inputs["proxied"].BoolValue()) {
						t.Fatal("record did not use the resolved Cloudflare zone and DNS-only mode")
					}
					if r.Inputs["type"].StringValue() != "CNAME" {
						t.Fatal("unexpected record type")
					}
					name := r.Inputs["name"].StringValue()
					if name != host && name != "_proof."+host {
						t.Fatalf("record outside the demo hostname: %s", name)
					}
					records++
				}
			}
			if len(permissions) != 0 {
				t.Fatalf("missing API Gateway Lambda permissions: %v", permissions)
			}
			if routes != 3 || records != 2 || mappings != 1 || authorizers != 1 {
				t.Fatalf("got %d routes, %d records, %d mappings, %d authorizers", routes, records, mappings, authorizers)
			}
		})
	}
}

func TestMobileHostnameRejectsUnrelatedZonesAndProduction(t *testing.T) {
	s := MobileSettings{BaseDomain: "components.email-verification.apexfission.com", DNSZone: "apexfission.com", DNSProvider: "route53"}
	for _, stack := range []string{"qa", "test-prod", "test-production", "test-qa-"} {
		if _, err := s.hostname(stack); err == nil {
			t.Fatalf("accepted %q", stack)
		}
	}
	s.DNSZone = "other.example"
	if _, err := s.hostname("test-qa"); err == nil {
		t.Fatal("accepted an unrelated DNS zone")
	}
}

func TestMobileDefaultsUseDeploymentDomain(t *testing.T) {
	for _, test := range []struct {
		baseDomain, stack, host string
	}{
		{"apexfission.com", "test-dev", "dev.components.email-verification.apexfission.com"},
		{"Example.test", "test-qa", "qa.components.email-verification.example.test"},
	} {
		s := (MobileSettings{DNSProvider: "route53"}).withOwnershipDefaults(test.baseDomain)
		got, err := s.hostname(test.stack)
		if err != nil || got != test.host {
			t.Fatalf("default hostname = %q, %v; want %q", got, err, test.host)
		}
	}

	override := MobileSettings{BaseDomain: "mobile.other.test", DNSProvider: "route53", DNSZone: "other.test"}
	if got := override.withOwnershipDefaults("example.test"); got != override {
		t.Fatalf("explicit mobile settings changed: %+v", got)
	}
	if got := (MobileSettings{}).withOwnershipDefaults("apexfission.com"); got.BaseDomain != "" {
		t.Fatalf("enabled mobile DNS without a provider: %+v", got)
	}
}

func TestMobileDNSLookupFailureIsActionableBeforeRegistration(t *testing.T) {
	m := &mocks{zoneLookupErr: errors.New("no matching Route 53 Hosted Zone found: fixture private diagnostic")}
	s := Settings{
		Domain: "example.test", Sender: "sender@example.test", Recipient: "tester@example.test",
		Archive: archive(t), Policy: config.Defaults(), Mobile: MobileSettings{DNSProvider: "route53"},
	}
	err := pulumi.RunErr(func(ctx *pulumi.Context) error { return Deploy(ctx, s) },
		pulumi.WithMocks("email-verification-testhost", "test-dev", m))
	if err == nil {
		t.Fatal("missing public hosted zone was accepted")
	}
	for _, part := range []string{"dev.components.email-verification.example.test", "no matching public Route 53 hosted zone", "mobileDNSZoneID", "mobileDNSProvider"} {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("missing %q in error: %v", part, err)
		}
	}
	if strings.Contains(err.Error(), "fixture private diagnostic") {
		t.Fatal("provider diagnostics leaked")
	}
	for _, r := range m.rows {
		if r.TypeToken != "pulumi:pulumi:Stack" {
			t.Fatalf("registered %s before checking mobile DNS", r.Name)
		}
	}
}

func TestCloudflareZoneLookupRejectsAmbiguousResultAndHidesProviderResponse(t *testing.T) {
	for _, tc := range []struct {
		name string
		mock *mocks
		want string
	}{
		{"permission failure", &mocks{zoneLookupErr: errors.New("fixture private diagnostic")}, "CLOUDFLARE_API_TOKEN"},
		{"no results", &mocks{cloudflareIDs: []string{}}, "did not return one active zone"},
		{"multiple results", &mocks{cloudflareIDs: []string{strings.Repeat("a", 32), strings.Repeat("b", 32)}}, "did not return one active zone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Settings{
				Domain: "example.test", Sender: "sender@example.test", Recipient: "tester@example.test",
				Archive: archive(t), Policy: config.Defaults(), Mobile: MobileSettings{DNSProvider: "cloudflare"},
			}
			err := pulumi.RunErr(func(ctx *pulumi.Context) error { return Deploy(ctx, s) },
				pulumi.WithMocks("email-verification-testhost", "test-dev", tc.mock))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "mobileDNSZoneID") ||
				strings.Contains(err.Error(), "fixture private diagnostic") {
				t.Fatalf("unexpected Cloudflare lookup error: %v", err)
			}
			for _, r := range tc.mock.rows {
				if r.TypeToken != "pulumi:pulumi:Stack" {
					t.Fatalf("registered %s before resolving a Cloudflare zone", r.Name)
				}
			}
		})
	}
}

func TestExplicitCloudflareZoneSkipsLookup(t *testing.T) {
	settings := MobileSettings{DNSProvider: "cloudflare", DNSZoneID: strings.Repeat("b", 32)}
	got, err := settings.resolveDNSZone(nil, "dev.example.test")
	if err != nil || got != settings {
		t.Fatalf("explicit zone changed: %+v, %v", got, err)
	}
}
