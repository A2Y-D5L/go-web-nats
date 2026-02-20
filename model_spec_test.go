//nolint:exhaustruct // ProjectSpec fixtures only set fields relevant to each test assertion.
package platform_test

import (
	"strings"
	"testing"

	platform "github.com/a2y-d5l/go-web-nats"
)

func TestModel_NormalizeProjectSpecDefaults(t *testing.T) {
	spec := platform.NormalizeProjectSpecForTest(platform.ProjectSpec{
		Name:    "hello",
		Runtime: "go_1.26",
		Environments: map[string]platform.EnvConfig{
			"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
		},
	})

	if spec.APIVersion != platform.ProjectAPIVersionForTest {
		t.Fatalf("expected apiVersion %q, got %q", platform.ProjectAPIVersionForTest, spec.APIVersion)
	}
	if spec.Kind != platform.ProjectKindForTest {
		t.Fatalf("expected kind %q, got %q", platform.ProjectKindForTest, spec.Kind)
	}
	if spec.NetworkPolicies.Ingress != "internal" || spec.NetworkPolicies.Egress != "internal" {
		t.Fatalf("unexpected default network policies: %#v", spec.NetworkPolicies)
	}
}

func TestModel_ValidateProjectSpecRejectsBadRuntime(t *testing.T) {
	spec := platform.NormalizeProjectSpecForTest(platform.ProjectSpec{
		Name:    "hello",
		Runtime: "go 1.26",
		Environments: map[string]platform.EnvConfig{
			"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
		},
		NetworkPolicies: platform.NetworkPolicies{
			Ingress: "internal",
			Egress:  "internal",
		},
	})
	err := platform.ValidateProjectSpecForTest(spec)
	if err == nil {
		t.Fatal("expected runtime validation error")
	}
	if !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("expected runtime error, got %v", err)
	}
}

func TestModel_RenderProjectConfigYAML(t *testing.T) {
	spec := platform.NormalizeProjectSpecForTest(platform.ProjectSpec{
		Name:    "hello",
		Runtime: "go_1.26",
		Environments: map[string]platform.EnvConfig{
			"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
		},
		NetworkPolicies: platform.NetworkPolicies{
			Ingress: "internal",
			Egress:  "internal",
		},
	})
	out := string(platform.RenderProjectConfigYAMLForTest(spec))
	if !strings.Contains(out, "apiVersion: "+platform.ProjectAPIVersionForTest) {
		t.Fatalf("missing apiVersion in yaml: %s", out)
	}
	if !strings.Contains(out, "runtime: go_1.26") {
		t.Fatalf("missing runtime in yaml: %s", out)
	}
	if !strings.Contains(out, "networkPolicies:") {
		t.Fatalf("missing networkPolicies in yaml: %s", out)
	}
}
