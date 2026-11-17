// Package feature declares verification infrastructure without importing Pulumi.
package feature

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lambdawalker/go.apexfission.aws.email-verification/config"
	composer "github.com/lambdawalker/go.infra.composer"
	"github.com/lambdawalker/go.infra.composer/configuration"
)

const Kind = "apexfission:email-verification:StorageAndSigner"

// Contract describes deployment outputs, not a remote endpoint or runtime service.
func Contract() composer.Contract {
	fields := map[string]composer.Field{}
	for _, k := range []string{"table_name", "table_arn", "key_id", "key_arn", "policy_json"} {
		fields[k] = composer.Field{Type: composer.String}
	}
	return composer.Contract{ID: "email.verification.infrastructure", Major: 1, Fields: fields}
}

// Declare owns only verifier storage and signing infrastructure. Host records,
// runtime compute, SES identity, trust publication and IAM remain host integrations.
func Declare(id string, policy config.Config) (*composer.Feature, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	values := map[string]composer.Value{}
	for k := range Contract().Fields {
		values[k] = composer.ResourceRef(id+".backend", k)
	}
	f := &composer.Feature{ID: id, Source: composer.Origin(), Resources: []composer.Resource{{
		ID: "backend", Kind: Kind, Source: composer.Origin(),
		Inputs: map[string]composer.Value{"policy_json": composer.Literal(string(raw))}, Outputs: Contract().Fields,
	}}, Offerings: []composer.Offering{{ID: "infrastructure", Contract: Contract(), Values: values}}}
	// Validate identifier and declaration without registering cloud resources.
	if _, err = composer.NewApplication("validation", f).Plan(composer.Environment{Name: "validation"}); err != nil {
		return nil, err
	}
	return f, nil
}

// ValidatePlan is mandatory before native deployment. Timing values must be
// known at planning time; unresolved references cannot bypass the safety gate.
func ValidatePlan(plan *composer.Plan) error {
	if !plan.Valid() {
		return fmt.Errorf("validated composer plan required")
	}
	for _, r := range plan.Resources() {
		if r.Kind != Kind {
			continue
		}
		n := r.Inputs["policy_json"].Node()
		raw, ok := n.Literal.(string)
		if n.Kind != "literal" || !ok || n.Secret {
			return fmt.Errorf("%s requires explicit nonsecret policy_json", r.ID)
		}
		if _, err := config.Load(strings.NewReader(raw)); err != nil {
			return fmt.Errorf("%s: %w", r.ID, err)
		}
	}
	return nil
}

// Configuration declares the effective policy delivery to an imported host.
// This validates a binding inventory; it does not install an environment reader.
func Configuration(environment, artifact, readerIdentity, writerIdentity string, policy config.Config) (configuration.Declaration, error) {
	if err := policy.Validate(); err != nil {
		return configuration.Declaration{}, err
	}
	raw, _ := json.Marshal(policy)
	initial := composer.Literal(string(raw))
	d := configuration.Declaration{Environment: environment, PolicyVersion: "email-verification-v1",
		Properties: []configuration.Property{{Name: "email_verification_policy", Type: composer.String, Category: configuration.Application, Sensitivity: configuration.Public, Required: true}},
		Consumers:  []configuration.Consumer{{Artifact: artifact, Property: "email_verification_policy", Readers: []string{"env"}}},
		Bindings: []configuration.Binding{{Artifact: artifact, Property: "email_verification_policy", Initial: &initial, OnMissing: configuration.MissingError, OnUnavailable: configuration.UnavailableError,
			Sources: []configuration.Source{{Name: "host-environment", Storage: "environment", Reader: "env", Location: artifact, Key: "EMAIL_VERIFICATION_POLICY", Scope: environment, ReaderIdentity: readerIdentity, WriterIdentity: writerIdentity, UpdateMethod: "host-rollout"}}}},
	}
	_, err := configuration.Validate(d)
	return d, err
}
