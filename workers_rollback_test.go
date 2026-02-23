//nolint:testpackage // Rollback worker tests use internal worker/store helpers.
package platform

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func rollbackWorkerSpec() ProjectSpec {
	return normalizeProjectSpec(ProjectSpec{
		APIVersion: projectAPIVersion,
		Kind:       projectKind,
		Name:       "rollback-worker",
		Runtime:    "go_1.26",
		Capabilities: []string{
			"http",
		},
		Environments: map[string]EnvConfig{
			"dev":     {Vars: map[string]string{"LOG_LEVEL": "debug"}},
			"staging": {Vars: map[string]string{"LOG_LEVEL": "info"}},
			"prod":    {Vars: map[string]string{"LOG_LEVEL": "warn"}},
		},
		NetworkPolicies: NetworkPolicies{
			Ingress: networkPolicyInternal,
			Egress:  networkPolicyInternal,
		},
	})
}

func seedRollbackCurrentEnvironment(
	t *testing.T,
	artifacts ArtifactStore,
	projectID string,
	spec ProjectSpec,
	environment string,
	image string,
) {
	t.Helper()
	_, err := runManifestApplyForEnvironment(
		context.Background(),
		nil,
		artifacts,
		ProjectOpMsg{
			OpID:              "op-seed-current",
			Kind:              OpCreate,
			ProjectID:         projectID,
			Spec:              spec,
			DeployEnv:         environment,
			FromEnv:           "",
			ToEnv:             "",
			RollbackReleaseID: "",
			RollbackEnv:       "",
			RollbackScope:     "",
			RollbackOverride:  false,
			Delivery: DeliveryLifecycle{
				Stage:       DeliveryStageDeploy,
				Environment: environment,
				FromEnv:     "",
				ToEnv:       "",
			},
			Err: "",
			At:  time.Now().UTC(),
		},
		spec,
		image,
		environment,
	)
	if err != nil {
		t.Fatalf("seed current environment manifests: %v", err)
	}
}

func TestWorkers_RollbackCodeOnlyKeepsCurrentConfigAndRestoresImage(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t)
	defer fixture.Close()

	const (
		projectID = "project-worker-rollback-code-only"
		opID      = "op-worker-rollback-code-only"
	)
	spec := rollbackWorkerSpec()
	artifacts := NewFSArtifacts(t.TempDir())
	putWorkerRuntimeProjectAndOp(t, fixture.store, projectID, opID, OpRollback, spec)

	seedRollbackCurrentEnvironment(t, artifacts, projectID, spec, "staging", "example.local/current:aaaa")
	writeRollbackReleaseArtifacts(
		t,
		artifacts,
		projectID,
		"releases/staging-to-prod/deployment.yaml",
		"releases/staging-to-prod/rendered.yaml",
		"example.local/rollback:bbbb",
		"warn",
	)
	release, err := fixture.store.PutRelease(context.Background(), ReleaseRecord{
		ID:                    "",
		ProjectID:             projectID,
		Environment:           "staging",
		OpID:                  "op-release-source",
		OpKind:                OpRelease,
		DeliveryStage:         DeliveryStageRelease,
		FromEnv:               "staging",
		ToEnv:                 "prod",
		Image:                 "example.local/rollback:bbbb",
		RenderedPath:          "releases/staging-to-prod/rendered.yaml",
		ConfigPath:            "releases/staging-to-prod/deployment.yaml",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("put rollback source release: %v", err)
	}

	_, err = promotionWorkerAction(context.Background(), fixture.store, artifacts, ProjectOpMsg{
		OpID:              opID,
		Kind:              OpRollback,
		ProjectID:         projectID,
		Spec:              spec,
		DeployEnv:         "",
		FromEnv:           "",
		ToEnv:             "",
		RollbackReleaseID: release.ID,
		RollbackEnv:       "staging",
		RollbackScope:     RollbackScopeCodeOnly,
		RollbackOverride:  false,
		Delivery: DeliveryLifecycle{
			Stage:       rollbackDeliveryStage("staging"),
			Environment: "staging",
			FromEnv:     "staging",
			ToEnv:       "staging",
		},
		Err: "",
		At:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("run rollback code_only worker action: %v", err)
	}

	deployment, err := artifacts.ReadFile(projectID, "deploy/staging/deployment.yaml")
	if err != nil {
		t.Fatalf("read deployed staging deployment: %v", err)
	}
	if image := parseDeploymentImage(deployment); image != "example.local/rollback:bbbb" {
		t.Fatalf("expected rollback image to be restored, got %q", image)
	}
	env := parseDeploymentEnvVars(deployment)
	if env["LOG_LEVEL"] != "info" {
		t.Fatalf("expected code_only rollback to keep current LOG_LEVEL=info, got %q", env["LOG_LEVEL"])
	}
}

func TestWorkers_RollbackCodeAndConfigRestoresConfigSnapshot(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t)
	defer fixture.Close()

	const (
		projectID = "project-worker-rollback-code-config"
		opID      = "op-worker-rollback-code-config"
	)
	spec := rollbackWorkerSpec()
	artifacts := NewFSArtifacts(t.TempDir())
	putWorkerRuntimeProjectAndOp(t, fixture.store, projectID, opID, OpRollback, spec)

	seedRollbackCurrentEnvironment(t, artifacts, projectID, spec, "staging", "example.local/current:aaaa")
	writeRollbackReleaseArtifacts(
		t,
		artifacts,
		projectID,
		"releases/staging-to-prod/deployment.yaml",
		"releases/staging-to-prod/rendered.yaml",
		"example.local/rollback:cccc",
		"warn",
	)
	release, err := fixture.store.PutRelease(context.Background(), ReleaseRecord{
		ID:                    "",
		ProjectID:             projectID,
		Environment:           "staging",
		OpID:                  "op-release-source",
		OpKind:                OpRelease,
		DeliveryStage:         DeliveryStageRelease,
		FromEnv:               "staging",
		ToEnv:                 "prod",
		Image:                 "example.local/rollback:cccc",
		RenderedPath:          "releases/staging-to-prod/rendered.yaml",
		ConfigPath:            "releases/staging-to-prod/deployment.yaml",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("put rollback source release: %v", err)
	}

	_, err = promotionWorkerAction(context.Background(), fixture.store, artifacts, ProjectOpMsg{
		OpID:              opID,
		Kind:              OpRollback,
		ProjectID:         projectID,
		Spec:              spec,
		DeployEnv:         "",
		FromEnv:           "",
		ToEnv:             "",
		RollbackReleaseID: release.ID,
		RollbackEnv:       "staging",
		RollbackScope:     RollbackScopeCodeAndConfig,
		RollbackOverride:  false,
		Delivery: DeliveryLifecycle{
			Stage:       rollbackDeliveryStage("staging"),
			Environment: "staging",
			FromEnv:     "staging",
			ToEnv:       "staging",
		},
		Err: "",
		At:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("run rollback code_and_config worker action: %v", err)
	}

	deployment, err := artifacts.ReadFile(projectID, "deploy/staging/deployment.yaml")
	if err != nil {
		t.Fatalf("read deployed staging deployment: %v", err)
	}
	if image := parseDeploymentImage(deployment); image != "example.local/rollback:cccc" {
		t.Fatalf("expected rollback image to be restored, got %q", image)
	}
	env := parseDeploymentEnvVars(deployment)
	if env["LOG_LEVEL"] != "warn" {
		t.Fatalf("expected code_and_config rollback to restore LOG_LEVEL=warn, got %q", env["LOG_LEVEL"])
	}
}

func TestWorkers_RollbackFullStateUsesRenderedSnapshot(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t)
	defer fixture.Close()

	const (
		projectID = "project-worker-rollback-full-state"
		opID      = "op-worker-rollback-full-state"
	)
	spec := rollbackWorkerSpec()
	artifacts := NewFSArtifacts(t.TempDir())
	putWorkerRuntimeProjectAndOp(t, fixture.store, projectID, opID, OpRollback, spec)

	seedRollbackCurrentEnvironment(t, artifacts, projectID, spec, "staging", "example.local/current:aaaa")
	writeRollbackReleaseArtifacts(
		t,
		artifacts,
		projectID,
		"releases/staging-to-prod/deployment.yaml",
		"releases/staging-to-prod/rendered.yaml",
		"example.local/rollback:dddd",
		"error",
	)
	release, err := fixture.store.PutRelease(context.Background(), ReleaseRecord{
		ID:                    "",
		ProjectID:             projectID,
		Environment:           "staging",
		OpID:                  "op-release-source",
		OpKind:                OpRelease,
		DeliveryStage:         DeliveryStageRelease,
		FromEnv:               "staging",
		ToEnv:                 "prod",
		Image:                 "example.local/rollback:dddd",
		RenderedPath:          "releases/staging-to-prod/rendered.yaml",
		ConfigPath:            "releases/staging-to-prod/deployment.yaml",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("put rollback source release: %v", err)
	}
	expectedRendered, err := artifacts.ReadFile(projectID, release.RenderedPath)
	if err != nil {
		t.Fatalf("read expected rendered snapshot: %v", err)
	}

	_, err = promotionWorkerAction(context.Background(), fixture.store, artifacts, ProjectOpMsg{
		OpID:              opID,
		Kind:              OpRollback,
		ProjectID:         projectID,
		Spec:              spec,
		DeployEnv:         "",
		FromEnv:           "",
		ToEnv:             "",
		RollbackReleaseID: release.ID,
		RollbackEnv:       "staging",
		RollbackScope:     RollbackScopeFullState,
		RollbackOverride:  false,
		Delivery: DeliveryLifecycle{
			Stage:       rollbackDeliveryStage("staging"),
			Environment: "staging",
			FromEnv:     "staging",
			ToEnv:       "staging",
		},
		Err: "",
		At:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("run rollback full_state worker action: %v", err)
	}

	deployedRendered, err := artifacts.ReadFile(projectID, "deploy/staging/rendered.yaml")
	if err != nil {
		t.Fatalf("read deployed rendered snapshot: %v", err)
	}
	if string(deployedRendered) != string(expectedRendered) {
		t.Fatalf("expected full_state rollback to restore rendered snapshot exactly")
	}
}

func TestWorkers_RollbackMissingScopeSnapshotDoesNotWriteDeployArtifacts(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t)
	defer fixture.Close()

	const (
		projectID = "project-worker-rollback-missing-snapshot"
		opID      = "op-worker-rollback-missing-snapshot"
	)
	spec := rollbackWorkerSpec()
	artifacts := NewFSArtifacts(t.TempDir())
	putWorkerRuntimeProjectAndOp(t, fixture.store, projectID, opID, OpRollback, spec)

	release, err := fixture.store.PutRelease(context.Background(), ReleaseRecord{
		ID:                    "",
		ProjectID:             projectID,
		Environment:           "staging",
		OpID:                  "op-release-source",
		OpKind:                OpPromote,
		DeliveryStage:         DeliveryStagePromote,
		FromEnv:               "dev",
		ToEnv:                 "staging",
		Image:                 "example.local/rollback:eeee",
		RenderedPath:          "promotions/dev-to-staging/rendered.yaml",
		ConfigPath:            "",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("put rollback source release: %v", err)
	}

	_, err = promotionWorkerAction(context.Background(), fixture.store, artifacts, ProjectOpMsg{
		OpID:              opID,
		Kind:              OpRollback,
		ProjectID:         projectID,
		Spec:              spec,
		DeployEnv:         "",
		FromEnv:           "",
		ToEnv:             "",
		RollbackReleaseID: release.ID,
		RollbackEnv:       "staging",
		RollbackScope:     RollbackScopeCodeAndConfig,
		RollbackOverride:  false,
		Delivery: DeliveryLifecycle{
			Stage:       rollbackDeliveryStage("staging"),
			Environment: "staging",
			FromEnv:     "staging",
			ToEnv:       "staging",
		},
		Err: "",
		At:  time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("expected rollback to fail when code_and_config snapshot is missing")
	}

	_, readErr := artifacts.ReadFile(projectID, "deploy/staging/rendered.yaml")
	if !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("expected no deploy artifact write on preflight failure, got err=%v", readErr)
	}
}
