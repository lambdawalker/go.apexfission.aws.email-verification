package teststack

import (
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
				DNSZone: "apexfission.com", DNSProvider: provider, DNSZoneID: strings.Repeat("a", 32)}
			s := Settings{Domain: "tags.example.test", Sender: "sender@example.test", Recipient: "tester@example.test",
				Archive: archive(t), Policy: config.Defaults(), Mobile: dns}
			err := pulumi.RunErr(func(ctx *pulumi.Context) error { return Deploy(ctx, s) }, pulumi.WithMocks("email-verification-testhost", "test-qa", m))
			if err != nil {
				t.Fatal(err)
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
