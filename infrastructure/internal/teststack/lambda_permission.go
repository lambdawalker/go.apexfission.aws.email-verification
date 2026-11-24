package teststack

import (
	"strings"

	"github.com/lambdawalker/go.infra.composer/adapters/pulumi/awsnames"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/lambda"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// Pulumi uses the logical name as the base of an auto-generated statement ID.
func newLambdaPermission(ctx *pulumi.Context, previousName string, args *lambda.PermissionArgs, opts ...pulumi.ResourceOption) (*lambda.Permission, error) {
	name := strings.ReplaceAll(previousName, ".", "-")
	if err := awsnames.ValidateLambdaStatementID(name); err != nil {
		return nil, err
	}
	return lambda.NewPermission(ctx, name, args, append(opts, pulumi.Aliases([]pulumi.Alias{
		{Name: pulumi.String(previousName)},
	}))...)
}
