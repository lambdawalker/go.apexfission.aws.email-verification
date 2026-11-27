package deploy_test

import (
	"bytes"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/lambdawalker/go.infra.composer/reporting"
	"github.com/lambdawalker/go.infra.composer/reporting/pulumicli"
)

// Exercise the reporting version actually pinned by deploy.sh, independently
// of the older Composer contract used by the infrastructure module.
func TestModuleAncestorSurvivesPreviewStateAndHTML(t *testing.T) {
	const scope = "urn:pulumi:test-ci::email-verification-testhost::"
	const moduleType = "apexfission:email-verification:Feature"
	const backendType = "apexfission:email-verification:StorageAndSigner"
	const runtimeType = "apexfission:email-verification:Runtime"
	const hostType = "apexfission:email-verification:TestHost"
	module := scope + moduleType + "::email-verification"
	backend := scope + moduleType + "$" + backendType + "::verification.backend"
	runtime := scope + moduleType + "$" + runtimeType + "::verification.runtime"
	host := scope + hostType + "::testhost.app"
	// Child-first order also checks that hierarchy does not depend on event order.
	resources := []map[string]any{
		{"urn": backend, "type": backendType, "custom": false, "parent": module},
		{"urn": runtime, "type": runtimeType, "custom": false, "parent": module},
		{"urn": host, "type": hostType, "custom": false},
		{"urn": module, "type": moduleType, "custom": false},
	}
	raw, err := json.Marshal(map[string]any{"deployment": map[string]any{"resources": resources}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := pulumicli.ReadState(bytes.NewReader(raw), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	for _, resource := range resources {
		event := map[string]any{"resourcePreEvent": map[string]any{"metadata": map[string]any{"urn": resource["urn"], "type": resource["type"], "op": "same", "new": resource}}}
		if err := json.NewEncoder(&stream).Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	preview, err := pulumicli.ApplyPreview(nil, &stream)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifacts := range [][]reporting.Artifact{state, preview} {
		found := map[string]bool{}
		for _, a := range artifacts {
			if !a.Component {
				t.Fatalf("lost component marker for %s", a.Name)
			}
			if (a.Key == backend || a.Key == runtime) && a.Parent != module {
				t.Fatalf("lost common ancestor for %s", a.Name)
			}
			if (a.Key == host || a.Key == module) && a.Parent != "" {
				t.Fatalf("unexpected parent for %s", a.Name)
			}
			found[a.Key] = true
		}
		if len(found) != 4 {
			t.Fatalf("incomplete hierarchy: %v", found)
		}
		var html bytes.Buffer
		if err := reporting.WriteHTML(&html, reporting.Report{Artifacts: artifacts}); err != nil {
			t.Fatal(err)
		}
		// The self-contained report encodes ancestry as nested details elements;
		// its tree and topology reconstruct parent relationships from this DOM.
		tags := regexp.MustCompile(`<details\b[^>]*>|</details>`)
		keys := regexp.MustCompile(`data-key="([^"]+)"`)
		var stack []string
		parents := map[string]string{}
		for _, tag := range tags.FindAllString(html.String(), -1) {
			if tag == "</details>" {
				if len(stack) == 0 {
					t.Fatal("unbalanced report hierarchy")
				}
				stack = stack[:len(stack)-1]
				continue
			}
			key := ""
			if match := keys.FindStringSubmatch(tag); match != nil {
				key = match[1]
				parent := ""
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i] != "" {
						parent = stack[i]
						break
					}
				}
				parents[key] = parent
			}
			stack = append(stack, key)
		}
		if len(stack) != 0 || len(parents) != 4 || parents[backend] != module || parents[runtime] != module || parents[host] != "" || parents[module] != "" {
			t.Fatalf("HTML report lost module hierarchy: %v", parents)
		}
	}
}
