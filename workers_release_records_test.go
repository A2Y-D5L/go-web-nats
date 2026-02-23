//nolint:testpackage // Worker release-record tests use internal worker/store helpers.
package platform

import (
	"context"
	"testing"
	"time"
)

func TestWorkers_DeploySuccessWritesReleaseRecord(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t)
	defer fixture.Close()

	const (
		projectID = "project-release-record-deploy"
		opID      = "op-release-record-deploy"
	)
	artifacts := NewFSArtifacts(t.TempDir())
	spec := workerRuntimeSpec("release-record-deploy")
	putWorkerRuntimeProjectAndOp(t, fixture.store, projectID, opID, OpDeploy, spec)

	if _, err := artifacts.WriteFile(
		projectID,
		imageBuildTagPath,
		[]byte("local/release-record:dev123\n"),
	); err != nil {
		t.Fatalf("write build image for deploy: %v", err)
	}

	_, err := deploymentWorkerAction(context.Background(), fixture.store, artifacts, ProjectOpMsg{
		OpID:      opID,
		Kind:      OpDeploy,
		ProjectID: projectID,
		Spec:      spec,
		DeployEnv: defaultDeployEnvironment,
		FromEnv:   "",
		ToEnv:     "",
		Delivery: DeliveryLifecycle{
			Stage:       DeliveryStageDeploy,
			Environment: defaultDeployEnvironment,
			FromEnv:     "",
			ToEnv:       "",
		},
		Err: "",
		At:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("run deploy worker action: %v", err)
	}

	page, err := fixture.store.listProjectReleases(
		context.Background(),
		projectID,
		defaultDeployEnvironment,
		projectReleaseListQuery{
			Limit:  5,
			Cursor: "",
		},
	)
	if err != nil {
		t.Fatalf("list deploy release records: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected 1 deploy release record, got %d", len(page.Items))
	}
	record := page.Items[0]
	if record.OpID != opID {
		t.Fatalf("expected release op_id %q, got %q", opID, record.OpID)
	}
	if record.OpKind != OpDeploy {
		t.Fatalf("expected release op_kind %q, got %q", OpDeploy, record.OpKind)
	}
	if record.DeliveryStage != DeliveryStageDeploy {
		t.Fatalf("expected delivery stage %q, got %q", DeliveryStageDeploy, record.DeliveryStage)
	}
	if record.Environment != defaultDeployEnvironment {
		t.Fatalf("expected release environment %q, got %q", defaultDeployEnvironment, record.Environment)
	}
	if record.ToEnv != defaultDeployEnvironment {
		t.Fatalf("expected release to_env %q, got %q", defaultDeployEnvironment, record.ToEnv)
	}
	if record.RenderedPath != "deploy/dev/rendered.yaml" {
		t.Fatalf("expected deploy rendered_path %q, got %q", "deploy/dev/rendered.yaml", record.RenderedPath)
	}
}

func TestWorkers_PromotionAndReleaseSuccessWriteReleaseRecords(t *testing.T) {
	fixture := newWorkerDeliveryFixture(t)
	defer fixture.Close()

	const projectID = "project-release-record-transition"
	artifacts := NewFSArtifacts(t.TempDir())
	spec := normalizeProjectSpec(ProjectSpec{
		APIVersion: projectAPIVersion,
		Kind:       projectKind,
		Name:       "release-record-transition",
		Runtime:    "go_1.26",
		Capabilities: []string{
			"http",
		},
		Environments: map[string]EnvConfig{
			"dev": {
				Vars: map[string]string{"LOG_LEVEL": "debug"},
			},
			"staging": {
				Vars: map[string]string{"LOG_LEVEL": "info"},
			},
			"prod": {
				Vars: map[string]string{"LOG_LEVEL": "warn"},
			},
		},
		NetworkPolicies: NetworkPolicies{
			Ingress: networkPolicyInternal,
			Egress:  networkPolicyInternal,
		},
	})

	seedMsg := ProjectOpMsg{
		OpID:      "op-release-record-seed-dev",
		Kind:      OpCreate,
		ProjectID: projectID,
		Spec:      spec,
		DeployEnv: defaultDeployEnvironment,
		FromEnv:   "",
		ToEnv:     "",
		Delivery: DeliveryLifecycle{
			Stage:       DeliveryStageDeploy,
			Environment: defaultDeployEnvironment,
			FromEnv:     "",
			ToEnv:       "",
		},
		Err: "",
		At:  time.Now().UTC(),
	}
	if _, err := runManifestApplyForEnvironment(
		context.Background(),
		nil,
		artifacts,
		seedMsg,
		spec,
		"local/release-record:seed123",
		defaultDeployEnvironment,
	); err != nil {
		t.Fatalf("seed dev deployment artifacts: %v", err)
	}

	const promoteOpID = "op-release-record-promote"
	putWorkerRuntimeProjectAndOp(t, fixture.store, projectID, promoteOpID, OpPromote, spec)
	_, err := promotionWorkerAction(context.Background(), fixture.store, artifacts, ProjectOpMsg{
		OpID:      promoteOpID,
		Kind:      OpPromote,
		ProjectID: projectID,
		Spec:      spec,
		DeployEnv: "",
		FromEnv:   "dev",
		ToEnv:     "staging",
		Delivery: DeliveryLifecycle{
			Stage:       DeliveryStagePromote,
			Environment: "",
			FromEnv:     "dev",
			ToEnv:       "staging",
		},
		Err: "",
		At:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("run promote worker action: %v", err)
	}

	stagingPage, err := fixture.store.listProjectReleases(
		context.Background(),
		projectID,
		"staging",
		projectReleaseListQuery{
			Limit:  5,
			Cursor: "",
		},
	)
	if err != nil {
		t.Fatalf("list staging release records: %v", err)
	}
	if len(stagingPage.Items) != 1 {
		t.Fatalf("expected 1 staging release record, got %d", len(stagingPage.Items))
	}
	stagingRecord := stagingPage.Items[0]
	if stagingRecord.OpID != promoteOpID {
		t.Fatalf("expected staging op_id %q, got %q", promoteOpID, stagingRecord.OpID)
	}
	if stagingRecord.OpKind != OpPromote {
		t.Fatalf("expected staging op_kind %q, got %q", OpPromote, stagingRecord.OpKind)
	}
	if stagingRecord.DeliveryStage != DeliveryStagePromote {
		t.Fatalf("expected staging delivery stage %q, got %q", DeliveryStagePromote, stagingRecord.DeliveryStage)
	}
	if stagingRecord.RenderedPath != "promotions/dev-to-staging/rendered.yaml" {
		t.Fatalf(
			"expected staging rendered_path %q, got %q",
			"promotions/dev-to-staging/rendered.yaml",
			stagingRecord.RenderedPath,
		)
	}

	const releaseOpID = "op-release-record-release"
	putWorkerRuntimeProjectAndOp(t, fixture.store, projectID, releaseOpID, OpRelease, spec)
	_, err = promotionWorkerAction(context.Background(), fixture.store, artifacts, ProjectOpMsg{
		OpID:      releaseOpID,
		Kind:      OpRelease,
		ProjectID: projectID,
		Spec:      spec,
		DeployEnv: "",
		FromEnv:   "staging",
		ToEnv:     "prod",
		Delivery: DeliveryLifecycle{
			Stage:       DeliveryStageRelease,
			Environment: "",
			FromEnv:     "staging",
			ToEnv:       "prod",
		},
		Err: "",
		At:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("run release worker action: %v", err)
	}

	prodPage, err := fixture.store.listProjectReleases(
		context.Background(),
		projectID,
		"prod",
		projectReleaseListQuery{
			Limit:  5,
			Cursor: "",
		},
	)
	if err != nil {
		t.Fatalf("list prod release records: %v", err)
	}
	if len(prodPage.Items) != 1 {
		t.Fatalf("expected 1 prod release record, got %d", len(prodPage.Items))
	}
	prodRecord := prodPage.Items[0]
	if prodRecord.OpID != releaseOpID {
		t.Fatalf("expected prod op_id %q, got %q", releaseOpID, prodRecord.OpID)
	}
	if prodRecord.OpKind != OpRelease {
		t.Fatalf("expected prod op_kind %q, got %q", OpRelease, prodRecord.OpKind)
	}
	if prodRecord.DeliveryStage != DeliveryStageRelease {
		t.Fatalf("expected prod delivery stage %q, got %q", DeliveryStageRelease, prodRecord.DeliveryStage)
	}
	if prodRecord.RenderedPath != "releases/staging-to-prod/rendered.yaml" {
		t.Fatalf(
			"expected prod rendered_path %q, got %q",
			"releases/staging-to-prod/rendered.yaml",
			prodRecord.RenderedPath,
		)
	}
}
