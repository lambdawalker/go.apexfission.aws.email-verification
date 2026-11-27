package infrastructure

import "github.com/pulumi/pulumi/sdk/v3/go/pulumi"

// FeatureGroup is a logical module ancestor shared by storage/signing and
// runtime components. It creates no AWS resource or separate deployment.
type FeatureGroup struct{ pulumi.ResourceState }

// NewFeatureGroup gives previews and reports an explicit module hierarchy.
// Use a unique name for each feature instance in the parent application.
func NewFeatureGroup(ctx *pulumi.Context, name string, opts ...pulumi.ResourceOption) (*FeatureGroup, error) {
	group := &FeatureGroup{}
	if err := ctx.RegisterComponentResource("apexfission:email-verification:Feature", name, group, opts...); err != nil {
		return nil, err
	}
	if err := ctx.RegisterResourceOutputs(group, pulumi.Map{}); err != nil {
		return nil, err
	}
	return group, nil
}

// ChildOptions places an existing top-level component under the feature while
// retaining its former root identity as an alias. Pulumi propagates this alias
// to descendants, preserving existing AWS resources and their physical names.
// Pass these options to Deploy and NewRuntime; host factories stay independent.
func (g *FeatureGroup) ChildOptions(opts ...pulumi.ResourceOption) []pulumi.ResourceOption {
	return append(append([]pulumi.ResourceOption{}, opts...), pulumi.Parent(g),
		pulumi.Aliases([]pulumi.Alias{{NoParent: pulumi.Bool(true)}}))
}
