package teststack

import (
	"fmt"
	"strings"

	"github.com/lambdawalker/go.infra.composer/adapters/pulumi/awsnames"
	names "github.com/lambdawalker/go.infra.composer/awsnames"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/acm"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/apigatewayv2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func deployMobileDomain(ctx *pulumi.Context, name string, s MobileSettings, host string, api *apigatewayv2.Api, stage *apigatewayv2.Stage, opts []pulumi.ResourceOption) error {
	if s.DNSProvider == "route53" && s.DNSZoneID == "" {
		zone, err := route53.LookupZone(ctx, &route53.LookupZoneArgs{
			Name: pulumi.StringRef(s.DNSZone + "."), PrivateZone: pulumi.BoolRef(false),
		})
		if err != nil {
			return fmt.Errorf("lookup public Route 53 zone %s: %w", s.DNSZone, err)
		}
		s.DNSZoneID = zone.ZoneId
	}
	cert, err := acm.NewCertificate(ctx, name+"-demo-certificate", &acm.CertificateArgs{
		DomainName: pulumi.String(host), ValidationMethod: pulumi.String("DNS"),
	}, opts...)
	if err != nil {
		return err
	}
	record := cert.DomainValidationOptions.ApplyT(func(options []acm.CertificateDomainValidationOption) (acm.CertificateDomainValidationOption, error) {
		for _, option := range options {
			if option.DomainName != nil && *option.DomainName == host && option.ResourceRecordType != nil &&
				*option.ResourceRecordType == "CNAME" && option.ResourceRecordName != nil && option.ResourceRecordValue != nil {
				return option, nil
			}
		}
		return acm.CertificateDomainValidationOption{}, fmt.Errorf("missing ACM CNAME for %s", host)
	}).(acm.CertificateDomainValidationOptionOutput)
	validationFQDN, err := mobileCNAME(ctx, name+"-demo-certificate-dns", s,
		record.ResourceRecordName().Elem(), record.ResourceRecordValue().Elem(), opts...)
	if err != nil {
		return err
	}
	validation, err := acm.NewCertificateValidation(ctx, name+"-demo-domain-validated", &acm.CertificateValidationArgs{
		CertificateArn: cert.Arn, ValidationRecordFqdns: pulumi.StringArray{validationFQDN},
	}, opts...)
	if err != nil {
		return err
	}
	domain, err := apigatewayv2.NewDomainName(ctx, name+"-demo-domain", &apigatewayv2.DomainNameArgs{
		DomainName: pulumi.String(host),
		DomainNameConfiguration: &apigatewayv2.DomainNameDomainNameConfigurationArgs{
			CertificateArn: validation.CertificateArn, EndpointType: pulumi.String("REGIONAL"), SecurityPolicy: pulumi.String("TLS_1_2"),
		},
	}, opts...)
	if err != nil {
		return err
	}
	mapping, err := apigatewayv2.NewApiMapping(ctx, name+"-demo-mapping", &apigatewayv2.ApiMappingArgs{
		ApiId: api.ID(), DomainName: domain.ID(), Stage: stage.Name,
	}, opts...)
	if err != nil {
		return err
	}
	_, err = mobileCNAME(ctx, name+"-demo-hostname", s, pulumi.String(host),
		domain.DomainNameConfiguration.TargetDomainName().Elem(),
		append(opts, pulumi.DependsOn([]pulumi.Resource{mapping}))...)
	return err
}

func mobileCNAME(ctx *pulumi.Context, name string, s MobileSettings, recordName, target pulumi.StringInput, opts ...pulumi.ResourceOption) (pulumi.StringOutput, error) {
	validName, err := awsnames.Input(names.DNSRecord, recordName)
	if err != nil {
		return pulumi.StringOutput{}, err
	}

	if s.DNSProvider == "cloudflare" {
		trimDot := func(input pulumi.StringInput) pulumi.StringOutput {
			return input.ToStringOutput().ApplyT(func(value string) string {
				return strings.TrimSuffix(value, ".")
			}).(pulumi.StringOutput)
		}
		// Register this small slice of the Cloudflare schema with the pinned provider.
		// The generated Go package includes the entire Cloudflare API.
		var record struct {
			pulumi.CustomResourceState
			Name pulumi.StringOutput `pulumi:"name"`
		}
		err := ctx.RegisterResource("cloudflare:index/dnsRecord:DnsRecord", name, pulumi.Map{
			"zoneId": pulumi.String(s.DNSZoneID), "name": trimDot(validName), "content": trimDot(target),
			"type": pulumi.String("CNAME"), "ttl": pulumi.Float64(300), "proxied": pulumi.Bool(false),
		}, &record, append(opts, pulumi.Version("6.20.0"))...)
		if err != nil {
			return pulumi.StringOutput{}, err
		}
		return record.Name, nil
	}
	record, err := route53.NewRecord(ctx, name, &route53.RecordArgs{
		ZoneId: pulumi.String(s.DNSZoneID), Name: validName, Type: pulumi.String("CNAME"),
		Ttl: pulumi.Int(300), Records: pulumi.StringArray{target},
	}, opts...)
	if err != nil {
		return pulumi.StringOutput{}, err
	}
	return record.Fqdn, nil
}
