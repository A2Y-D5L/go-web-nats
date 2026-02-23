//nolint:testpackage,exhaustruct // Release API tests require internal store fixtures and concise records.
package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type projectReleaseListResponseForTest struct {
	Items      []ReleaseRecord `json:"items"`
	NextCursor string          `json:"next_cursor"`
}

func TestAPI_ProjectReleaseListSupportsEnvironmentLimitAndCursor(t *testing.T) {
	fixture := newProjectReleaseAPIFixture(t)
	defer fixture.Close()

	firstStaging, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:            "",
		ProjectID:     fixture.projectID,
		Environment:   "staging",
		OpID:          "op-release-list-staging-1",
		OpKind:        OpPromote,
		DeliveryStage: DeliveryStagePromote,
		FromEnv:       "dev",
		ToEnv:         "staging",
		Image:         "local/release-list:1111",
		RenderedPath:  "promotions/dev-to-staging/rendered.yaml",
		CreatedAt:     time.Now().UTC().Add(-3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put first staging release: %v", err)
	}

	secondStaging, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:            "",
		ProjectID:     fixture.projectID,
		Environment:   "staging",
		OpID:          "op-release-list-staging-2",
		OpKind:        OpPromote,
		DeliveryStage: DeliveryStagePromote,
		FromEnv:       "dev",
		ToEnv:         "staging",
		Image:         "local/release-list:2222",
		RenderedPath:  "promotions/dev-to-staging/rendered.yaml",
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put second staging release: %v", err)
	}

	_, err = fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:            "",
		ProjectID:     fixture.projectID,
		Environment:   "prod",
		OpID:          "op-release-list-prod-1",
		OpKind:        OpRelease,
		DeliveryStage: DeliveryStageRelease,
		FromEnv:       "staging",
		ToEnv:         "prod",
		Image:         "local/release-list:3333",
		RenderedPath:  "releases/staging-to-prod/rendered.yaml",
		CreatedAt:     time.Now().UTC().Add(-1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put prod release: %v", err)
	}

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	pageOne := fetchProjectReleaseListForTest(
		t,
		srv.Client(),
		fmt.Sprintf(
			"%s/api/projects/%s/releases?environment=staging&limit=1",
			srv.URL,
			fixture.projectID,
		),
	)
	if len(pageOne.Items) != 1 {
		t.Fatalf("expected 1 release item on page one, got %d", len(pageOne.Items))
	}
	got := pageOne.Items[0]
	if got.ID != secondStaging.ID {
		t.Fatalf("expected latest staging release %q, got %q", secondStaging.ID, got.ID)
	}
	if got.ProjectID != fixture.projectID {
		t.Fatalf("expected project_id %q, got %q", fixture.projectID, got.ProjectID)
	}
	if got.Environment != "staging" {
		t.Fatalf("expected environment staging, got %q", got.Environment)
	}
	if got.OpID == "" || got.OpKind == "" || got.DeliveryStage == "" || got.CreatedAt.IsZero() {
		t.Fatalf("expected release list item required fields, got %#v", got)
	}
	if pageOne.NextCursor != secondStaging.ID {
		t.Fatalf("expected next_cursor %q, got %q", secondStaging.ID, pageOne.NextCursor)
	}

	pageTwo := fetchProjectReleaseListForTest(
		t,
		srv.Client(),
		fmt.Sprintf(
			"%s/api/projects/%s/releases?environment=staging&limit=1&cursor=%s",
			srv.URL,
			fixture.projectID,
			pageOne.NextCursor,
		),
	)
	if len(pageTwo.Items) != 1 {
		t.Fatalf("expected 1 release item on page two, got %d", len(pageTwo.Items))
	}
	if pageTwo.Items[0].ID != firstStaging.ID {
		t.Fatalf("expected older staging release %q, got %q", firstStaging.ID, pageTwo.Items[0].ID)
	}
	if pageTwo.NextCursor != "" {
		t.Fatalf("expected empty terminal next_cursor, got %q", pageTwo.NextCursor)
	}
}

func TestAPI_ProjectReleaseDetailReturnsNotFoundAndSuccess(t *testing.T) {
	fixture := newProjectReleaseAPIFixture(t)
	defer fixture.Close()

	release, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:            "",
		ProjectID:     fixture.projectID,
		Environment:   "prod",
		OpID:          "op-release-detail-prod",
		OpKind:        OpRelease,
		DeliveryStage: DeliveryStageRelease,
		FromEnv:       "staging",
		ToEnv:         "prod",
		Image:         "local/release-detail:7777",
		RenderedPath:  "releases/staging-to-prod/rendered.yaml",
		CreatedAt:     time.Now().UTC().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put detail release: %v", err)
	}

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	notFoundResp, err := srv.Client().Get(fmt.Sprintf(
		"%s/api/projects/%s/releases/%s",
		srv.URL,
		fixture.projectID,
		"release-missing",
	))
	if err != nil {
		t.Fatalf("request missing release detail: %v", err)
	}
	defer notFoundResp.Body.Close()
	if notFoundResp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(notFoundResp.Body)
		t.Fatalf("expected 404 for missing release detail, got %d body=%q", notFoundResp.StatusCode, string(body))
	}

	okResp, err := srv.Client().Get(fmt.Sprintf(
		"%s/api/projects/%s/releases/%s",
		srv.URL,
		fixture.projectID,
		release.ID,
	))
	if err != nil {
		t.Fatalf("request release detail: %v", err)
	}
	defer okResp.Body.Close()
	if okResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(okResp.Body)
		t.Fatalf("expected 200 for release detail, got %d body=%q", okResp.StatusCode, string(body))
	}

	var body ReleaseRecord
	if err = json.NewDecoder(okResp.Body).Decode(&body); err != nil {
		t.Fatalf("decode release detail response: %v", err)
	}
	if body.ID != release.ID {
		t.Fatalf("expected release id %q, got %q", release.ID, body.ID)
	}
	if body.ProjectID != fixture.projectID {
		t.Fatalf("expected project_id %q, got %q", fixture.projectID, body.ProjectID)
	}
	if body.Environment != "prod" {
		t.Fatalf("expected environment prod, got %q", body.Environment)
	}
}

func TestAPI_ProjectReleaseCompareReturnsDeterministicSummary(t *testing.T) {
	fixture := newProjectReleaseAPIFixture(t)
	defer fixture.Close()

	writeCompareReleaseArtifacts := func(path string, body string) {
		t.Helper()
		if _, err := fixture.api.artifacts.WriteFile(fixture.projectID, path, []byte(body)); err != nil {
			t.Fatalf("write compare fixture artifact %q: %v", path, err)
		}
	}

	fromRendered := strings.Join([]string{
		"apiVersion: apps/v1",
		"kind: Deployment",
		"metadata:",
		"  name: app",
		"  creationTimestamp: 2026-02-20T00:00:00Z",
		"spec:",
		"  template:",
		"    spec:",
		"      containers:",
		"        - name: app",
		"          image: example.local/compare:1111",
		"          env:",
		"            - name: LOG_LEVEL",
		"              value: info",
		"---",
		"apiVersion: v1",
		"kind: Service",
		"metadata:",
		"  name: app",
		"spec:",
		"  selector:",
		"    app: app",
		"",
	}, "\n")
	toRendered := strings.ReplaceAll(fromRendered, "2026-02-20T00:00:00Z", "2026-02-21T00:00:00Z")
	fromConfig := fromRendered
	toConfig := strings.ReplaceAll(
		strings.ReplaceAll(fromRendered, "compare:1111", "compare:2222"),
		"value: info",
		"value: warn",
	)

	writeCompareReleaseArtifacts("promotions/dev-to-staging/rendered.yaml", fromRendered)
	writeCompareReleaseArtifacts("promotions/dev-to-staging/deployment.yaml", fromConfig)
	writeCompareReleaseArtifacts("releases/staging-to-prod/rendered.yaml", toRendered)
	writeCompareReleaseArtifacts("releases/staging-to-prod/deployment.yaml", toConfig)

	fromRelease, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:                    "",
		ProjectID:             fixture.projectID,
		Environment:           "staging",
		OpID:                  "op-release-compare-from",
		OpKind:                OpPromote,
		DeliveryStage:         DeliveryStagePromote,
		FromEnv:               "dev",
		ToEnv:                 "staging",
		Image:                 "example.local/compare:1111",
		RenderedPath:          "promotions/dev-to-staging/rendered.yaml",
		ConfigPath:            "promotions/dev-to-staging/deployment.yaml",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC().Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put compare from release: %v", err)
	}
	toRelease, err := fixture.api.store.PutRelease(context.Background(), ReleaseRecord{
		ID:                    "",
		ProjectID:             fixture.projectID,
		Environment:           "staging",
		OpID:                  "op-release-compare-to",
		OpKind:                OpRelease,
		DeliveryStage:         DeliveryStageRelease,
		FromEnv:               "staging",
		ToEnv:                 "prod",
		Image:                 "example.local/compare:2222",
		RenderedPath:          "releases/staging-to-prod/rendered.yaml",
		ConfigPath:            "releases/staging-to-prod/deployment.yaml",
		RollbackSafe:          rollbackSafeDefaultPtr(),
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Now().UTC().Add(-1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("put compare to release: %v", err)
	}

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	requestCompare := func() ReleaseCompareResponse {
		t.Helper()
		url := fmt.Sprintf(
			"%s/api/projects/%s/releases/compare?from=%s&to=%s",
			srv.URL,
			fixture.projectID,
			fromRelease.ID,
			toRelease.ID,
		)
		resp, requestErr := srv.Client().Get(url)
		if requestErr != nil {
			t.Fatalf("request release compare: %v", requestErr)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200 for compare, got %d body=%q", resp.StatusCode, string(body))
		}
		var payload ReleaseCompareResponse
		if decodeErr := json.NewDecoder(resp.Body).Decode(&payload); decodeErr != nil {
			t.Fatalf("decode compare response: %v", decodeErr)
		}
		return payload
	}

	first := requestCompare()
	second := requestCompare()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("expected deterministic compare response, got\nfirst=%#v\nsecond=%#v", first, second)
	}
	if !first.ImageDelta.Changed {
		t.Fatalf("expected image delta to be changed, got %#v", first.ImageDelta)
	}
	if !first.ConfigDelta.Changed ||
		len(first.ConfigDelta.Updated) != 1 ||
		first.ConfigDelta.Updated[0] != "LOG_LEVEL" {
		t.Fatalf("expected config delta LOG_LEVEL update, got %#v", first.ConfigDelta)
	}
	if first.RenderedDelta.Changed {
		t.Fatalf("expected rendered delta unchanged after noise filtering, got %#v", first.RenderedDelta)
	}
	if !strings.Contains(first.Summary, "Image changed: true") {
		t.Fatalf("unexpected compare summary: %q", first.Summary)
	}
}

type projectReleaseAPIFixture struct {
	api       *API
	projectID string
	close     func()
}

func newProjectReleaseAPIFixture(t *testing.T) *projectReleaseAPIFixture {
	t.Helper()

	workerFixture := newWorkerDeliveryFixture(t)

	projectID := "project-release-api"
	now := time.Now().UTC()
	project := Project{
		ID:        projectID,
		CreatedAt: now,
		UpdatedAt: now,
		Spec: normalizeProjectSpec(ProjectSpec{
			APIVersion: projectAPIVersion,
			Kind:       projectKind,
			Name:       "release-api-project",
			Runtime:    "go_1.26",
			Capabilities: []string{
				"http",
			},
			Environments: map[string]EnvConfig{
				"staging": {
					Vars: map[string]string{
						"LOG_LEVEL": "info",
					},
				},
				"prod": {
					Vars: map[string]string{
						"LOG_LEVEL": "warn",
					},
				},
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
		t.Fatalf("put release API project fixture: %v", err)
	}

	return &projectReleaseAPIFixture{
		api: &API{
			nc:                  workerFixture.nc,
			store:               workerFixture.store,
			artifacts:           NewFSArtifacts(t.TempDir()),
			waiters:             newWaiterHub(),
			opEvents:            nil,
			opHeartbeatInterval: 0,
			sourceTriggerMu:     sync.Mutex{},
			projectStartLocksMu: sync.Mutex{},
			projectStartLocks:   map[string]*sync.Mutex{},
		},
		projectID: projectID,
		close: func() {
			workerFixture.close()
		},
	}
}

func (f *projectReleaseAPIFixture) Close() {
	if f == nil || f.close == nil {
		return
	}
	f.close()
}

func fetchProjectReleaseListForTest(
	t *testing.T,
	client *http.Client,
	url string,
) projectReleaseListResponseForTest {
	t.Helper()

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("request project release list: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%q", resp.StatusCode, string(body))
	}

	var page projectReleaseListResponseForTest
	if err = json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode project release list response: %v", err)
	}
	return page
}
