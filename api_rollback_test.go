//nolint:testpackage // Rollback API tests exercise internal fixtures and concise setup.
package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

func writeRollbackReleaseArtifacts(
	t *testing.T,
	artifacts ArtifactStore,
	projectID string,
	deploymentPath string,
	renderedPath string,
	image string,
	logLevel string,
) {
	t.Helper()
	deployment := []byte(
		"apiVersion: apps/v1\n" +
			"kind: Deployment\n" +
			"metadata:\n" +
			"  name: app\n" +
			"spec:\n" +
			"  template:\n" +
			"    spec:\n" +
			"      containers:\n" +
			"        - name: app\n" +
			"          image: " + image + "\n" +
			"          env:\n" +
			"            - name: LOG_LEVEL\n" +
			"              value: " + logLevel + "\n",
	)
	rendered := []byte(
		string(deployment) +
			"---\n" +
			"apiVersion: v1\n" +
			"kind: Service\n" +
			"metadata:\n" +
			"  name: app\n" +
			"spec:\n" +
			"  selector:\n" +
			"    app: app\n",
	)
	if _, err := artifacts.WriteFile(projectID, deploymentPath, deployment); err != nil {
		t.Fatalf("write rollback deployment fixture: %v", err)
	}
	if _, err := artifacts.WriteFile(projectID, renderedPath, rendered); err != nil {
		t.Fatalf("write rollback rendered fixture: %v", err)
	}
}

func postRollbackRequest(
	t *testing.T,
	client *http.Client,
	url string,
	body map[string]any,
) (*http.Response, string) {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal rollback request: %v", err)
	}
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("post rollback request: %v", err)
	}
	raw, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("read rollback response: %v", readErr)
	}
	_ = resp.Body.Close()
	return resp, string(raw)
}

func rollbackBlockerCodes(preview RollbackPreviewResponse) []string {
	codes := make([]string, 0, len(preview.Blockers))
	for _, blocker := range preview.Blockers {
		codes = append(codes, blocker.Code)
	}
	slices.Sort(codes)
	return codes
}

func TestAPI_RollbackPreviewBlocksWhenScopeConfigSnapshotMissing(t *testing.T) {
	fixture := newProjectReleaseAPIFixture(t)
	defer fixture.Close()

	release, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:                    "",
		ProjectID:             fixture.projectID,
		Environment:           "staging",
		OpID:                  "op-rollback-preview-missing-config",
		OpKind:                OpPromote,
		DeliveryStage:         DeliveryStagePromote,
		FromEnv:               "dev",
		ToEnv:                 "staging",
		Image:                 "example.local/rollback:1111",
		RenderedPath:          "promotions/dev-to-staging/rendered.yaml",
		ConfigPath:            "",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("put rollback release fixture: %v", err)
	}

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()
	resp, raw := postRollbackRequest(t, srv.Client(), srv.URL+"/api/events/rollback/preview", map[string]any{
		"project_id":  fixture.projectID,
		"environment": "staging",
		"release_id":  release.ID,
		"scope":       string(RollbackScopeCodeAndConfig),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 preview, got %d body=%q", resp.StatusCode, raw)
	}
	var preview RollbackPreviewResponse
	if err = json.Unmarshal([]byte(raw), &preview); err != nil {
		t.Fatalf("decode rollback preview response: %v", err)
	}
	if preview.Ready {
		t.Fatalf("expected preview to be blocked, got %#v", preview)
	}
	if !slices.Contains(rollbackBlockerCodes(preview), rollbackBlockerConfigMissing) {
		t.Fatalf("expected blocker %q, got %#v", rollbackBlockerConfigMissing, rollbackBlockerCodes(preview))
	}
}

func TestAPI_RollbackPreviewReadyIncludesCompare(t *testing.T) {
	fixture := newProjectReleaseAPIFixture(t)
	defer fixture.Close()

	writeRollbackReleaseArtifacts(
		t,
		fixture.api.artifacts,
		fixture.projectID,
		"releases/staging-to-prod/deployment.yaml",
		"releases/staging-to-prod/rendered.yaml",
		"example.local/rollback:2222",
		"warn",
	)
	writeRollbackReleaseArtifacts(
		t,
		fixture.api.artifacts,
		fixture.projectID,
		"promotions/dev-to-staging/deployment.yaml",
		"promotions/dev-to-staging/rendered.yaml",
		"example.local/rollback:1111",
		"info",
	)

	_, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:                    "",
		ProjectID:             fixture.projectID,
		Environment:           "staging",
		OpID:                  "op-rollback-preview-current",
		OpKind:                OpPromote,
		DeliveryStage:         DeliveryStagePromote,
		FromEnv:               "dev",
		ToEnv:                 "staging",
		Image:                 "example.local/rollback:1111",
		RenderedPath:          "promotions/dev-to-staging/rendered.yaml",
		ConfigPath:            "promotions/dev-to-staging/deployment.yaml",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put current release fixture: %v", err)
	}
	targetRelease, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:                    "",
		ProjectID:             fixture.projectID,
		Environment:           "staging",
		OpID:                  "op-rollback-preview-target",
		OpKind:                OpRelease,
		DeliveryStage:         DeliveryStageRelease,
		FromEnv:               "staging",
		ToEnv:                 "prod",
		Image:                 "example.local/rollback:2222",
		RenderedPath:          "releases/staging-to-prod/rendered.yaml",
		ConfigPath:            "releases/staging-to-prod/deployment.yaml",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC().Add(-1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put target release fixture: %v", err)
	}

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()
	resp, raw := postRollbackRequest(t, srv.Client(), srv.URL+"/api/events/rollback/preview", map[string]any{
		"project_id":  fixture.projectID,
		"environment": "staging",
		"release_id":  targetRelease.ID,
		"scope":       string(RollbackScopeCodeOnly),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 preview, got %d body=%q", resp.StatusCode, raw)
	}
	var preview RollbackPreviewResponse
	if err = json.Unmarshal([]byte(raw), &preview); err != nil {
		t.Fatalf("decode rollback preview response: %v", err)
	}
	if !preview.Ready {
		t.Fatalf("expected preview ready=true, got %#v", preview)
	}
	if len(preview.Blockers) != 0 {
		t.Fatalf("expected no blockers, got %#v", preview.Blockers)
	}
	if preview.Compare == nil {
		t.Fatalf("expected compare payload, got %#v", preview)
	}
	if preview.Compare.FromRelease == nil || preview.Compare.ToRelease == nil {
		t.Fatalf("expected compare release details, got %#v", preview.Compare)
	}
}

func TestAPI_RollbackExecuteReturnsNotFoundForUnknownProject(t *testing.T) {
	fixture := newProjectReleaseAPIFixture(t)
	defer fixture.Close()

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()
	resp, raw := postRollbackRequest(t, srv.Client(), srv.URL+"/api/events/rollback", map[string]any{
		"project_id":  "project-missing",
		"environment": "staging",
		"release_id":  "release-missing",
		"scope":       string(RollbackScopeCodeOnly),
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%q", resp.StatusCode, raw)
	}
}

func TestAPI_RollbackExecuteBlocksWhenPreflightFails(t *testing.T) {
	fixture := newProjectReleaseAPIFixture(t)
	defer fixture.Close()

	release, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:                    "",
		ProjectID:             fixture.projectID,
		Environment:           "staging",
		OpID:                  "op-rollback-exec-blocked",
		OpKind:                OpPromote,
		DeliveryStage:         DeliveryStagePromote,
		FromEnv:               "dev",
		ToEnv:                 "staging",
		Image:                 "example.local/rollback:3333",
		RenderedPath:          "promotions/dev-to-staging/rendered.yaml",
		ConfigPath:            "",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("put blocked rollback release: %v", err)
	}

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()
	resp, raw := postRollbackRequest(t, srv.Client(), srv.URL+"/api/events/rollback", map[string]any{
		"project_id":  fixture.projectID,
		"environment": "staging",
		"release_id":  release.ID,
		"scope":       string(RollbackScopeCodeAndConfig),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 blocked preflight, got %d body=%q", resp.StatusCode, raw)
	}
	var preview RollbackPreviewResponse
	if err = json.Unmarshal([]byte(raw), &preview); err != nil {
		t.Fatalf("decode blocked rollback response: %v", err)
	}
	if preview.Ready {
		t.Fatalf("expected ready=false for blocked rollback, got %#v", preview)
	}
}

func TestAPI_RollbackExecuteAcceptsWhenPreflightPasses(t *testing.T) {
	fixture := newProjectReleaseAPIFixture(t)
	defer fixture.Close()

	writeRollbackReleaseArtifacts(
		t,
		fixture.api.artifacts,
		fixture.projectID,
		"releases/staging-to-prod/deployment.yaml",
		"releases/staging-to-prod/rendered.yaml",
		"example.local/rollback:4444",
		"warn",
	)
	release, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:                    "",
		ProjectID:             fixture.projectID,
		Environment:           "staging",
		OpID:                  "op-rollback-exec-pass",
		OpKind:                OpRelease,
		DeliveryStage:         DeliveryStageRelease,
		FromEnv:               "staging",
		ToEnv:                 "prod",
		Image:                 "example.local/rollback:4444",
		RenderedPath:          "releases/staging-to-prod/rendered.yaml",
		ConfigPath:            "releases/staging-to-prod/deployment.yaml",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("put rollback release fixture: %v", err)
	}

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()
	resp, raw := postRollbackRequest(t, srv.Client(), srv.URL+"/api/events/rollback", map[string]any{
		"project_id":  fixture.projectID,
		"environment": "staging",
		"release_id":  release.ID,
		"scope":       string(RollbackScopeCodeOnly),
	})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202 accepted rollback, got %d body=%q", resp.StatusCode, raw)
	}

	var accepted struct {
		Accepted bool      `json:"accepted"`
		Project  Project   `json:"project"`
		Op       Operation `json:"op"`
	}
	if err = json.Unmarshal([]byte(raw), &accepted); err != nil {
		t.Fatalf("decode accepted rollback response: %v", err)
	}
	if !accepted.Accepted {
		t.Fatalf("expected accepted=true, got %#v", accepted)
	}
	if accepted.Op.Kind != OpRollback {
		t.Fatalf("expected op kind rollback, got %q", accepted.Op.Kind)
	}
	if accepted.Op.Delivery.Environment != "staging" ||
		accepted.Op.Delivery.FromEnv != "staging" ||
		accepted.Op.Delivery.ToEnv != "staging" {
		t.Fatalf("unexpected rollback delivery metadata: %#v", accepted.Op.Delivery)
	}
}
