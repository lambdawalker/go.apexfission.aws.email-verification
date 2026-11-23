package teststack

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/apigatewayv2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/cognito"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/lambda"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type MobileSettings struct {
	BaseDomain, DNSProvider, DNSZone, DNSZoneID string
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var demoEnvironment = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,29}[a-z0-9])?$`)

func (s MobileSettings) hostname(stack string) (string, error) {
	environment, ok := strings.CutPrefix(stack, "test-")
	if !ok || !demoEnvironment.MatchString(environment) || environment == "prod" || environment == "production" {
		return "", fmt.Errorf("mobile endpoint requires a non-production test-<environment> stack")
	}
	host := environment + "." + s.BaseDomain
	for _, name := range []string{host, s.DNSZone} {
		if len(name) > 253 || !strings.Contains(name, ".") {
			return "", fmt.Errorf("invalid mobile DNS name %q", name)
		}
		for _, part := range strings.Split(name, ".") {
			if !dnsLabel.MatchString(part) {
				return "", fmt.Errorf("invalid mobile DNS name %q", name)
			}
		}
	}
	if !strings.HasSuffix(host, "."+s.DNSZone) {
		return "", fmt.Errorf("mobile hostname must be within the configured DNS zone")
	}
	switch s.DNSProvider {
	case "route53":
	case "cloudflare":
		if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(s.DNSZoneID) {
			return "", fmt.Errorf("mobileDNSZoneID must be the Cloudflare zone ID")
		}
	default:
		return "", fmt.Errorf("mobileDNSProvider must be route53 or cloudflare")
	}
	return host, nil
}

func deployMobile(ctx *pulumi.Context, name string, s MobileSettings, host string, function *lambda.Function, opts []pulumi.ResourceOption) error {
	pool, err := cognito.NewUserPool(ctx, name+"-demo-users", &cognito.UserPoolArgs{
		AdminCreateUserConfig: &cognito.UserPoolAdminCreateUserConfigArgs{AllowAdminCreateUserOnly: pulumi.Bool(true)},
	}, opts...)
	if err != nil {
		return err
	}
	client, err := cognito.NewUserPoolClient(ctx, name+"-demo-android", &cognito.UserPoolClientArgs{
		UserPoolId: pool.ID(), GenerateSecret: pulumi.Bool(false),
		ExplicitAuthFlows:          pulumi.ToStringArray([]string{"ALLOW_USER_SRP_AUTH", "ALLOW_REFRESH_TOKEN_AUTH"}),
		PreventUserExistenceErrors: pulumi.String("ENABLED"),
	}, opts...)
	if err != nil {
		return err
	}
	issuer := pulumi.Sprintf("https://%s", pool.Endpoint)
	api, err := apigatewayv2.NewApi(ctx, name+"-demo-api", &apigatewayv2.ApiArgs{
		ProtocolType: pulumi.String("HTTP"), DisableExecuteApiEndpoint: pulumi.Bool(true),
	}, opts...)
	if err != nil {
		return err
	}
	stage, err := apigatewayv2.NewStage(ctx, name+"-demo-stage", &apigatewayv2.StageArgs{
		ApiId: api.ID(), Name: pulumi.String("$default"), AutoDeploy: pulumi.Bool(true),
		DefaultRouteSettings: &apigatewayv2.StageDefaultRouteSettingsArgs{
			ThrottlingBurstLimit: pulumi.Int(10), ThrottlingRateLimit: pulumi.Float64(5),
		},
	}, opts...)
	if err != nil {
		return err
	}
	auth, err := apigatewayv2.NewAuthorizer(ctx, name+"-demo-auth", &apigatewayv2.AuthorizerArgs{
		ApiId: api.ID(), AuthorizerType: pulumi.String("JWT"),
		IdentitySources: pulumi.ToStringArray([]string{"$request.header.Authorization"}),
		JwtConfiguration: &apigatewayv2.AuthorizerJwtConfigurationArgs{
			Issuer: issuer, Audiences: pulumi.StringArray{client.ID().ToStringOutput()},
		},
	}, opts...)
	if err != nil {
		return err
	}
	integration, err := apigatewayv2.NewIntegration(ctx, name+"-demo-invoke", &apigatewayv2.IntegrationArgs{
		ApiId: api.ID(), IntegrationType: pulumi.String("AWS_PROXY"),
		IntegrationUri: function.InvokeArn, PayloadFormatVersion: pulumi.String("2.0"),
	}, opts...)
	if err != nil {
		return err
	}
	for _, action := range []string{"start", "confirm", "status"} {
		route := "/v1/verification/" + action
		_, err = apigatewayv2.NewRoute(ctx, name+"-demo-"+action, &apigatewayv2.RouteArgs{
			ApiId: api.ID(), RouteKey: pulumi.String("POST " + route), AuthorizationType: pulumi.String("JWT"),
			AuthorizerId: auth.ID(), Target: pulumi.Sprintf("integrations/%s", integration.ID()),
		}, opts...)
		if err != nil {
			return err
		}
		_, err = lambda.NewPermission(ctx, name+"-demo-permission-"+action, &lambda.PermissionArgs{
			Action: pulumi.String("lambda:InvokeFunction"), Function: function.Name,
			Principal: pulumi.String("apigateway.amazonaws.com"),
			SourceArn: pulumi.Sprintf("%s/$default/POST%s", api.ExecutionArn, route),
		}, opts...)
		if err != nil {
			return err
		}
	}
	if err := deployMobileDomain(ctx, name, s, host, api, stage, opts); err != nil {
		return err
	}
	ctx.Export("testHostMobileURL", pulumi.String("https://"+host))
	ctx.Export("testHostMobileUserPoolID", pool.ID())
	ctx.Export("testHostMobileClientID", client.ID())
	ctx.Export("testHostMobileIssuer", issuer)
	return nil
}
