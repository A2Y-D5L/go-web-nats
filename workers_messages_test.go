//nolint:exhaustruct // Message fixtures omit irrelevant fields to keep test intent obvious.
package platform_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	platform "github.com/a2y-d5l/go-web-nats"
)

func TestWorkers_ResultCarriesOpKindAndSpecForNextWorker(t *testing.T) {
	in := platform.WorkerResultMsg{
		OpID:      "op-1",
		Kind:      platform.OpCreate,
		ProjectID: "proj-1",
		Spec: platform.ProjectSpec{
			APIVersion: platform.ProjectAPIVersionForTest,
			Kind:       platform.ProjectKindForTest,
			Name:       "svc",
			Runtime:    "go_1.26",
			Environments: map[string]platform.EnvConfig{
				"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
			},
			NetworkPolicies: platform.NetworkPolicies{
				Ingress: "internal",
				Egress:  "internal",
			},
		},
	}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal worker result: %v", err)
	}

	var opMsg platform.ProjectOpMsg
	unmarshalErr := json.Unmarshal(b, &opMsg)
	if unmarshalErr != nil {
		t.Fatalf("unmarshal as project op: %v", unmarshalErr)
	}

	if opMsg.Kind != platform.OpCreate {
		t.Fatalf("expected kind %q, got %q", platform.OpCreate, opMsg.Kind)
	}
	if opMsg.Spec.Name != "svc" || opMsg.Spec.Runtime != "go_1.26" {
		t.Fatalf("unexpected spec: %#v", opMsg.Spec)
	}
}

func TestWorkers_RenderKustomizedProjectManifests(t *testing.T) {
	spec := platform.ProjectSpec{
		APIVersion: platform.ProjectAPIVersionForTest,
		Kind:       platform.ProjectKindForTest,
		Name:       "svc",
		Runtime:    "go_1.26",
		Environments: map[string]platform.EnvConfig{
			"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
		},
		NetworkPolicies: platform.NetworkPolicies{
			Ingress: "internal",
			Egress:  "internal",
		},
	}

	deployment, service, rendered, err := platform.RenderKustomizedProjectManifestsForTest(
		spec,
		"local/svc:abc12345",
	)
	if err != nil {
		t.Fatalf("render kustomized manifests: %v", err)
	}
	if !strings.Contains(deployment, "kind: Deployment") {
		t.Fatalf("rendered deployment missing kind: %s", deployment)
	}
	if !strings.Contains(deployment, "image: local/svc:abc12345") {
		t.Fatalf("rendered deployment missing image tag: %s", deployment)
	}
	if !strings.Contains(service, "kind: Service") {
		t.Fatalf("rendered service missing kind: %s", service)
	}
	if !strings.Contains(rendered, "kind: Deployment") || !strings.Contains(rendered, "kind: Service") {
		t.Fatalf("combined rendered manifest missing resources: %s", rendered)
	}
}

func TestWorkers_ManifestApplyWritesKustomizeTreeAndDevRenderOnly(t *testing.T) {
	artifacts := platform.NewFSArtifacts(t.TempDir())
	spec := platform.ProjectSpec{
		APIVersion: platform.ProjectAPIVersionForTest,
		Kind:       platform.ProjectKindForTest,
		Name:       "svc",
		Runtime:    "go_1.26",
		Environments: map[string]platform.EnvConfig{
			"dev":     {Vars: map[string]string{"LOG_LEVEL": "debug"}},
			"staging": {Vars: map[string]string{"LOG_LEVEL": "warn"}},
		},
		NetworkPolicies: platform.NetworkPolicies{
			Ingress: "internal",
			Egress:  "internal",
		},
	}
	msg := platform.ProjectOpMsg{
		OpID:      "op-deploy-dev",
		Kind:      platform.OpCreate,
		ProjectID: "proj-manifests-dev",
		Spec:      spec,
		At:        time.Now().UTC(),
	}

	message, touched, err := platform.RunManifestApplyForTest(
		context.Background(),
		artifacts,
		msg,
		spec,
		"local/svc:dev12345",
		"dev",
	)
	if err != nil {
		t.Fatalf("run manifest apply: %v", err)
	}
	if !strings.Contains(message, "dev environment") {
		t.Fatalf("unexpected deploy message: %q", message)
	}
	for _, want := range []string{
		"repos/manifests/base/deployment.yaml",
		"repos/manifests/base/service.yaml",
		"repos/manifests/base/kustomization.yaml",
		"repos/manifests/overlays/dev/kustomization.yaml",
		"repos/manifests/overlays/dev/deployment-patch.yaml",
		"repos/manifests/overlays/staging/kustomization.yaml",
		"deploy/dev/rendered.yaml",
	} {
		if !slices.Contains(touched, want) {
			t.Fatalf("expected touched artifact %q, got %v", want, touched)
		}
	}
	if slices.Contains(touched, "deploy/staging/rendered.yaml") {
		t.Fatalf("staging manifests must not be rendered during deploy apply: %v", touched)
	}
	devMarker, readErr := artifacts.ReadFile(msg.ProjectID, "repos/manifests/overlays/dev/image.txt")
	if readErr != nil {
		t.Fatalf("read dev image marker: %v", readErr)
	}
	if strings.TrimSpace(string(devMarker)) != "local/svc:dev12345" {
		t.Fatalf("unexpected dev image marker: %q", strings.TrimSpace(string(devMarker)))
	}
	devDeployment, readErr := artifacts.ReadFile(msg.ProjectID, "deploy/dev/deployment.yaml")
	if readErr != nil {
		t.Fatalf("read dev deployment: %v", readErr)
	}
	if !strings.Contains(string(devDeployment), "image: local/svc:dev12345") {
		t.Fatalf("expected dev deployment image tag, got: %s", string(devDeployment))
	}
}

func TestWorkers_ManifestPromotionRendersHigherEnvOnlyDuringPromotion(t *testing.T) {
	artifacts := platform.NewFSArtifacts(t.TempDir())
	spec := platform.ProjectSpec{
		APIVersion: platform.ProjectAPIVersionForTest,
		Kind:       platform.ProjectKindForTest,
		Name:       "svc",
		Runtime:    "go_1.26",
		Environments: map[string]platform.EnvConfig{
			"dev":     {Vars: map[string]string{"LOG_LEVEL": "debug"}},
			"staging": {Vars: map[string]string{"LOG_LEVEL": "warn"}},
		},
		NetworkPolicies: platform.NetworkPolicies{
			Ingress: "internal",
			Egress:  "internal",
		},
	}

	deployMsg := platform.ProjectOpMsg{
		OpID:      "op-deploy-dev",
		Kind:      platform.OpCreate,
		ProjectID: "proj-promote",
		Spec:      spec,
		At:        time.Now().UTC(),
	}
	_, _, err := platform.RunManifestApplyForTest(
		context.Background(),
		artifacts,
		deployMsg,
		spec,
		"local/svc:dev98765",
		"dev",
	)
	if err != nil {
		t.Fatalf("run initial dev deploy: %v", err)
	}
	if _, readErr := artifacts.ReadFile(deployMsg.ProjectID, "deploy/staging/rendered.yaml"); readErr == nil {
		t.Fatal("staging rendered manifest should not exist before promotion")
	}

	promoteMsg := platform.ProjectOpMsg{
		OpID:      "op-promote-staging",
		Kind:      platform.OpPromote,
		ProjectID: deployMsg.ProjectID,
		Spec:      spec,
		At:        time.Now().UTC(),
	}
	message, touched, err := platform.RunManifestPromotionForTest(
		context.Background(),
		artifacts,
		promoteMsg,
		spec,
		"dev",
		"staging",
	)
	if err != nil {
		t.Fatalf("run promotion: %v", err)
	}
	if !strings.Contains(message, "dev to staging") {
		t.Fatalf("unexpected promotion message: %q", message)
	}
	for _, want := range []string{
		"deploy/staging/rendered.yaml",
		"promotions/dev-to-staging/rendered.yaml",
	} {
		if !slices.Contains(touched, want) {
			t.Fatalf("expected promotion artifact %q, got %v", want, touched)
		}
	}
	stagingDeployment, readErr := artifacts.ReadFile(deployMsg.ProjectID, "deploy/staging/deployment.yaml")
	if readErr != nil {
		t.Fatalf("read staging deployment: %v", readErr)
	}
	if !strings.Contains(string(stagingDeployment), "image: local/svc:dev98765") {
		t.Fatalf("expected promoted staging deployment image tag, got: %s", string(stagingDeployment))
	}
}

func TestWorkers_ManifestReleaseRendersProductionAndWritesReleaseArtifacts(t *testing.T) {
	artifacts := platform.NewFSArtifacts(t.TempDir())
	spec := platform.ProjectSpec{
		APIVersion: platform.ProjectAPIVersionForTest,
		Kind:       platform.ProjectKindForTest,
		Name:       "svc",
		Runtime:    "go_1.26",
		Environments: map[string]platform.EnvConfig{
			"dev":  {Vars: map[string]string{"LOG_LEVEL": "debug"}},
			"prod": {Vars: map[string]string{"LOG_LEVEL": "warn"}},
		},
		NetworkPolicies: platform.NetworkPolicies{
			Ingress: "internal",
			Egress:  "internal",
		},
	}

	deployMsg := platform.ProjectOpMsg{
		OpID:      "op-deploy-dev-release",
		Kind:      platform.OpCreate,
		ProjectID: "proj-release",
		Spec:      spec,
		At:        time.Now().UTC(),
	}
	_, _, err := platform.RunManifestApplyForTest(
		context.Background(),
		artifacts,
		deployMsg,
		spec,
		"local/svc:dev24680",
		"dev",
	)
	if err != nil {
		t.Fatalf("run initial dev deploy: %v", err)
	}
	if _, readErr := artifacts.ReadFile(deployMsg.ProjectID, "deploy/prod/rendered.yaml"); readErr == nil {
		t.Fatal("prod rendered manifest should not exist before release")
	}

	releaseMsg := platform.ProjectOpMsg{
		OpID:      "op-release-prod",
		Kind:      platform.OpRelease,
		ProjectID: deployMsg.ProjectID,
		Spec:      spec,
		At:        time.Now().UTC(),
	}
	message, touched, err := platform.RunManifestPromotionForTest(
		context.Background(),
		artifacts,
		releaseMsg,
		spec,
		"dev",
		"prod",
	)
	if err != nil {
		t.Fatalf("run release: %v", err)
	}
	if !strings.Contains(message, "released manifests from dev to prod") {
		t.Fatalf("unexpected release message: %q", message)
	}
	for _, want := range []string{
		"deploy/prod/rendered.yaml",
		"releases/dev-to-prod/rendered.yaml",
	} {
		if !slices.Contains(touched, want) {
			t.Fatalf("expected release artifact %q, got %v", want, touched)
		}
	}
	if slices.Contains(touched, "promotions/dev-to-prod/rendered.yaml") {
		t.Fatalf("release should not write promotion artifact path: %v", touched)
	}
	prodDeployment, readErr := artifacts.ReadFile(deployMsg.ProjectID, "deploy/prod/deployment.yaml")
	if readErr != nil {
		t.Fatalf("read prod deployment: %v", readErr)
	}
	if !strings.Contains(string(prodDeployment), "image: local/svc:dev24680") {
		t.Fatalf("expected released prod deployment image tag, got: %s", string(prodDeployment))
	}
}
