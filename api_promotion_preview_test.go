//nolint:testpackage,exhaustruct // Promotion preview API tests use internal runtime fixtures and concise setup.
package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type promotionPreviewFixture struct {
	api       *API
	projectID string
	artifacts *FSArtifacts
	close     func()
}

func newPromotionPreviewFixture(t *testing.T) *promotionPreviewFixture {
	t.Helper()

	workerFixture := newWorkerDeliveryFixture(t)
	projectID := "project-promotion-preview"
	now := time.Now().UTC()

	project := Project{
		ID:        projectID,
		CreatedAt: now,
		UpdatedAt: now,
		Spec: normalizeProjectSpec(ProjectSpec{
			APIVersion: projectAPIVersion,
			Kind:       projectKind,
			Name:       "promotion-preview-app",
			Runtime:    "go_1.26",
			Capabilities: []string{
				"http",
			},
			Environments: map[string]EnvConfig{
				"staging": {Vars: map[string]string{"LOG_LEVEL": "info"}},
				"prod":    {Vars: map[string]string{"LOG_LEVEL": "warn"}},
			},
			NetworkPolicies: NetworkPolicies{
				Ingress: networkPolicyInternal,
				Egress:  networkPolicyInternal,
			},
		}),
		Status: ProjectStatus{
			Phase:      projectPhaseReady,
			UpdatedAt:  now,
			LastOpID:   "",
			LastOpKind: "",
			Message:    "ready",
		},
	}
	if err := workerFixture.store.PutProject(context.Background(), project); err != nil {
		t.Fatalf("put preview project fixture: %v", err)
	}

	artifacts := NewFSArtifacts(t.TempDir())
	return &promotionPreviewFixture{
		api: &API{
			nc:                  workerFixture.nc,
			store:               workerFixture.store,
			artifacts:           artifacts,
			waiters:             newWaiterHub(),
			opEvents:            nil,
			opHeartbeatInterval: 0,
			sourceTriggerMu:     sync.Mutex{},
			projectStartLocksMu: sync.Mutex{},
			projectStartLocks:   map[string]*sync.Mutex{},
		},
		projectID: projectID,
		artifacts: artifacts,
		close:     workerFixture.close,
	}
}

func (f *promotionPreviewFixture) Close() {
	if f == nil || f.close == nil {
		return
	}
	f.close()
}

func writePreviewDeploymentImage(
	t *testing.T,
	artifacts *FSArtifacts,
	projectID string,
	env string,
	image string,
) {
	t.Helper()
	body := fmt.Sprintf(
		`apiVersion: apps/v1
kind: Deployment
metadata:
  name: app
spec:
  template:
    spec:
      containers:
        - name: app
          image: %s
`,
		strings.TrimSpace(image),
	)
	if _, err := artifacts.WriteFile(
		projectID,
		fmt.Sprintf("deploy/%s/deployment.yaml", normalizeEnvironmentName(env)),
		[]byte(body),
	); err != nil {
		t.Fatalf("write deployment image fixture: %v", err)
	}
}

func postPromotionPreview(
	t *testing.T,
	client *http.Client,
	baseURL string,
	body map[string]any,
) (int, PromotionPreviewResponse, string) {
	t.Helper()

	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal promotion preview payload: %v", err)
	}
	resp, err := client.Post(
		baseURL+"/api/events/promotion/preview",
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatalf("request promotion preview: %v", err)
	}
	defer resp.Body.Close()

	rawBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("read promotion preview body: %v", readErr)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, PromotionPreviewResponse{}, strings.TrimSpace(string(rawBody))
	}

	var preview PromotionPreviewResponse
	if decodeErr := json.Unmarshal(rawBody, &preview); decodeErr != nil {
		t.Fatalf("decode promotion preview response: %v", decodeErr)
	}
	return resp.StatusCode, preview, strings.TrimSpace(string(rawBody))
}

func blockerCodes(preview PromotionPreviewResponse) []string {
	codes := make([]string, 0, len(preview.Blockers))
	for _, blocker := range preview.Blockers {
		codes = append(codes, strings.TrimSpace(blocker.Code))
	}
	slices.Sort(codes)
	return codes
}

func TestAPI_PromotionPreviewReturnsChangeSummaryAndRolloutPlan(t *testing.T) {
	fixture := newPromotionPreviewFixture(t)
	defer fixture.Close()

	writePreviewDeploymentImage(
		t,
		fixture.artifacts,
		fixture.projectID,
		"dev",
		"example.local/promotion-preview:dev123",
	)
	writePreviewDeploymentImage(
		t,
		fixture.artifacts,
		fixture.projectID,
		"staging",
		"example.local/promotion-preview:old999",
	)

	_, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:            "",
		ProjectID:     fixture.projectID,
		Environment:   "dev",
		OpID:          "op-preview-source",
		OpKind:        OpDeploy,
		DeliveryStage: DeliveryStageDeploy,
		FromEnv:       "",
		ToEnv:         "dev",
		Image:         "example.local/promotion-preview:dev123",
		RenderedPath:  "deploy/dev/rendered.yaml",
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put source release fixture: %v", err)
	}
	_, err = fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:            "",
		ProjectID:     fixture.projectID,
		Environment:   "staging",
		OpID:          "op-preview-target",
		OpKind:        OpPromote,
		DeliveryStage: DeliveryStagePromote,
		FromEnv:       "dev",
		ToEnv:         "staging",
		Image:         "example.local/promotion-preview:old999",
		RenderedPath:  "promotions/dev-to-staging/rendered.yaml",
		CreatedAt:     time.Now().UTC().Add(-1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put target release fixture: %v", err)
	}

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	status, preview, raw := postPromotionPreview(
		t,
		srv.Client(),
		srv.URL,
		map[string]any{
			"project_id": fixture.projectID,
			"from_env":   "dev",
			"to_env":     "staging",
		},
	)
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", status, raw)
	}
	if preview.Action != "promote" {
		t.Fatalf("expected action promote, got %q", preview.Action)
	}
	if len(preview.Blockers) != 0 {
		t.Fatalf("expected no blockers, got %#v", preview.Blockers)
	}
	if preview.SourceRelease == nil || preview.SourceRelease.Environment != "dev" {
		t.Fatalf("expected source release in dev, got %#v", preview.SourceRelease)
	}
	if preview.TargetRelease == nil || preview.TargetRelease.Environment != "staging" {
		t.Fatalf("expected target release in staging, got %#v", preview.TargetRelease)
	}
	if len(preview.RolloutPlan) != 4 {
		t.Fatalf("expected 4 rollout stages, got %d", len(preview.RolloutPlan))
	}
	if preview.RolloutPlan[0] != promotionStepPlan || preview.RolloutPlan[3] != promotionStepFinalize {
		t.Fatalf("unexpected rollout plan: %#v", preview.RolloutPlan)
	}
	if !strings.Contains(preview.ChangeSummary, "dev") || !strings.Contains(preview.ChangeSummary, "staging") {
		t.Fatalf("expected change summary to mention env move, got %q", preview.ChangeSummary)
	}
}

func TestAPI_PromotionPreviewBlocksForActiveOperation(t *testing.T) {
	fixture := newPromotionPreviewFixture(t)
	defer fixture.Close()

	writePreviewDeploymentImage(
		t,
		fixture.artifacts,
		fixture.projectID,
		"dev",
		"example.local/promotion-preview:active",
	)
	_, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:            "",
		ProjectID:     fixture.projectID,
		Environment:   "dev",
		OpID:          "op-preview-active-source",
		OpKind:        OpDeploy,
		DeliveryStage: DeliveryStageDeploy,
		FromEnv:       "",
		ToEnv:         "dev",
		Image:         "example.local/promotion-preview:active",
		RenderedPath:  "deploy/dev/rendered.yaml",
		CreatedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("put source release fixture: %v", err)
	}

	runningOp := Operation{
		ID:        "op-preview-active-running",
		Kind:      OpCI,
		ProjectID: fixture.projectID,
		Delivery:  DeliveryLifecycle{},
		Requested: time.Now().UTC().Add(-30 * time.Second),
		Finished:  time.Time{},
		Status:    opStatusRunning,
		Error:     "",
		Steps:     []OpStep{},
	}
	if err = fixture.api.store.PutOp(context.Background(), runningOp); err != nil {
		t.Fatalf("put active op fixture: %v", err)
	}
	project, err := fixture.api.store.GetProject(context.Background(), fixture.projectID)
	if err != nil {
		t.Fatalf("get project fixture: %v", err)
	}
	project.Status.LastOpID = runningOp.ID
	project.Status.LastOpKind = string(runningOp.Kind)
	project.Status.UpdatedAt = time.Now().UTC()
	project.Status.Message = "running"
	if err = fixture.api.store.PutProject(context.Background(), project); err != nil {
		t.Fatalf("put project active-op status fixture: %v", err)
	}

	preview := requestPromotionPreviewForTest(t, fixture, map[string]any{
		"project_id": fixture.projectID,
		"from_env":   "dev",
		"to_env":     "staging",
	})
	assertPromotionPreviewHasBlocker(t, preview, transitionBlockerActiveOperation)
}

func TestAPI_PromotionPreviewBlocksForInvalidTransition(t *testing.T) {
	fixture := newPromotionPreviewFixture(t)
	defer fixture.Close()

	preview := requestPromotionPreviewForTest(t, fixture, map[string]any{
		"project_id": fixture.projectID,
		"from_env":   "staging",
		"to_env":     "staging",
	})
	assertPromotionPreviewHasBlocker(t, preview, transitionBlockerInvalidMove)
}

func TestAPI_PromotionPreviewBlocksForMissingSourceImage(t *testing.T) {
	fixture := newPromotionPreviewFixture(t)
	defer fixture.Close()

	_, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:            "",
		ProjectID:     fixture.projectID,
		Environment:   "dev",
		OpID:          "op-preview-source-missing-image",
		OpKind:        OpDeploy,
		DeliveryStage: DeliveryStageDeploy,
		FromEnv:       "",
		ToEnv:         "dev",
		Image:         "example.local/promotion-preview:no-render",
		RenderedPath:  "deploy/dev/rendered.yaml",
		CreatedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("put source release fixture: %v", err)
	}

	preview := requestPromotionPreviewForTest(t, fixture, map[string]any{
		"project_id": fixture.projectID,
		"from_env":   "dev",
		"to_env":     "staging",
	})
	assertPromotionPreviewHasBlocker(t, preview, transitionBlockerSourceImage)
}

func TestAPI_PromotionPreviewBlocksForUndeliveredSource(t *testing.T) {
	fixture := newPromotionPreviewFixture(t)
	defer fixture.Close()

	writePreviewDeploymentImage(
		t,
		fixture.artifacts,
		fixture.projectID,
		"dev",
		"example.local/promotion-preview:rendered-but-no-release",
	)

	preview := requestPromotionPreviewForTest(t, fixture, map[string]any{
		"project_id": fixture.projectID,
		"from_env":   "dev",
		"to_env":     "staging",
	})
	assertPromotionPreviewHasBlocker(t, preview, transitionBlockerSourceDelivery)
}

func TestAPI_PromotionPreviewBlocksForUnavailableTarget(t *testing.T) {
	fixture := newPromotionPreviewFixture(t)
	defer fixture.Close()

	writePreviewDeploymentImage(
		t,
		fixture.artifacts,
		fixture.projectID,
		"dev",
		"example.local/promotion-preview:target-missing",
	)
	_, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:            "",
		ProjectID:     fixture.projectID,
		Environment:   "dev",
		OpID:          "op-preview-target-unavailable-source",
		OpKind:        OpDeploy,
		DeliveryStage: DeliveryStageDeploy,
		FromEnv:       "",
		ToEnv:         "dev",
		Image:         "example.local/promotion-preview:target-missing",
		RenderedPath:  "deploy/dev/rendered.yaml",
		CreatedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("put source release fixture: %v", err)
	}

	preview := requestPromotionPreviewForTest(t, fixture, map[string]any{
		"project_id": fixture.projectID,
		"from_env":   "dev",
		"to_env":     "qa",
	})
	assertPromotionPreviewHasBlocker(t, preview, transitionBlockerTargetMissing)
}

func requestPromotionPreviewForTest(
	t *testing.T,
	fixture *promotionPreviewFixture,
	body map[string]any,
) PromotionPreviewResponse {
	t.Helper()
	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	_, preview, _ := postPromotionPreview(t, srv.Client(), srv.URL, body)
	return preview
}

func assertPromotionPreviewHasBlocker(
	t *testing.T,
	preview PromotionPreviewResponse,
	blockerCode string,
) {
	t.Helper()
	if !slices.Contains(blockerCodes(preview), blockerCode) {
		t.Fatalf("expected blocker code %q, got %#v", blockerCode, blockerCodes(preview))
	}
}
