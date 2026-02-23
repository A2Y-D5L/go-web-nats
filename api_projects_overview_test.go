//nolint:testpackage,exhaustruct // Overview API tests require internal runtime helpers and concise fixtures.
package platform

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type projectOverviewEnvForTest struct {
	Name             string `json:"name"`
	HealthStatus     string `json:"health_status"`
	DeliveryState    string `json:"delivery_state"`
	RunningImage     string `json:"running_image"`
	DeliveryType     string `json:"delivery_type"`
	DeliveryPath     string `json:"delivery_path"`
	ConfigReadiness  string `json:"config_readiness"`
	SecretsReadiness string `json:"secrets_readiness"`
	LastDeliveryAt   string `json:"last_delivery_at"`
}

type projectOverviewForTest struct {
	Summary      string                      `json:"summary"`
	Environments []projectOverviewEnvForTest `json:"environments"`
}

type projectOverviewResponseForTest struct {
	Project  Project                `json:"project"`
	Overview projectOverviewForTest `json:"overview"`
}

func TestAPI_ProjectOverviewReturnsReadModelAndDeterministicOrder(t *testing.T) {
	api, projectID, secretValue := newProjectOverviewReadModelFixture(t)

	srv := httptest.NewServer(api.routes())
	defer srv.Close()

	body := fetchProjectOverviewForTest(t, srv.Client(), srv.URL, projectID)
	assertProjectOverviewReadModelForTest(t, body.Overview)
	assertOverviewExcludesRawEnvValuesForTest(t, body.Overview, secretValue)
}

func newProjectOverviewReadModelFixture(t *testing.T) (*API, string, string) {
	t.Helper()

	workerFixture := newWorkerDeliveryFixture(t)
	t.Cleanup(workerFixture.Close)

	projectID := "project-overview-read-model"
	secretValue := "SUPER_SECRET_SHOULD_NOT_APPEAR_IN_OVERVIEW"
	now := time.Now().UTC()

	spec := normalizeProjectSpec(ProjectSpec{
		APIVersion: projectAPIVersion,
		Kind:       projectKind,
		Name:       "overview-app",
		Runtime:    "go_1.26",
		Capabilities: []string{
			"http",
		},
		Environments: map[string]EnvConfig{
			"prod": {
				Vars: map[string]string{
					"DB_PASSWORD": secretValue,
				},
			},
			"qa": {
				Vars: map[string]string{
					"LOG_LEVEL": "debug",
				},
			},
			"staging": {
				Vars: map[string]string{
					"LOG_LEVEL": "info",
				},
			},
		},
		NetworkPolicies: NetworkPolicies{
			Ingress: networkPolicyInternal,
			Egress:  networkPolicyInternal,
		},
	})

	recentOp := Operation{
		ID:        "op-overview-promote",
		Kind:      OpPromote,
		ProjectID: projectID,
		Delivery: DeliveryLifecycle{
			Stage:       DeliveryStagePromote,
			Environment: "",
			FromEnv:     "dev",
			ToEnv:       "staging",
		},
		Requested: now.Add(-5 * time.Minute),
		Finished:  now.Add(-4 * time.Minute),
		Status:    opStatusDone,
		Error:     "",
		Steps:     []OpStep{},
	}
	if err := workerFixture.store.PutOp(context.Background(), recentOp); err != nil {
		t.Fatalf("put overview recent op fixture: %v", err)
	}

	project := Project{
		ID:        projectID,
		CreatedAt: now.Add(-20 * time.Minute),
		UpdatedAt: now.Add(-3 * time.Minute),
		Spec:      spec,
		Status: ProjectStatus{
			Phase:      projectPhaseReady,
			UpdatedAt:  now.Add(-3 * time.Minute),
			LastOpID:   recentOp.ID,
			LastOpKind: string(recentOp.Kind),
			Message:    "ready",
		},
	}
	if err := workerFixture.store.PutProject(context.Background(), project); err != nil {
		t.Fatalf("put overview project fixture: %v", err)
	}

	artifacts := NewFSArtifacts(t.TempDir())
	writeOverviewArtifactForTest(
		t,
		artifacts,
		projectID,
		"build/image.txt",
		[]byte("example.local/overview-app:abc123\n"),
	)
	writeOverviewArtifactForTest(t, artifacts, projectID, "deploy/dev/rendered.yaml", []byte("kind: ConfigMap\n"))
	writeOverviewArtifactForTest(
		t,
		artifacts,
		projectID,
		"promotions/dev-to-staging/rendered.yaml",
		[]byte("kind: ConfigMap\n"),
	)

	return &API{
		nc:                  nil,
		store:               workerFixture.store,
		artifacts:           artifacts,
		waiters:             nil,
		opEvents:            nil,
		opHeartbeatInterval: 0,
		sourceTriggerMu:     sync.Mutex{},
		projectStartLocksMu: sync.Mutex{},
		projectStartLocks:   map[string]*sync.Mutex{},
	}, projectID, secretValue
}

func writeOverviewArtifactForTest(
	t *testing.T,
	artifacts *FSArtifacts,
	projectID string,
	path string,
	data []byte,
) {
	t.Helper()
	if _, err := artifacts.WriteFile(projectID, path, data); err != nil {
		t.Fatalf("write overview artifact %q: %v", path, err)
	}
}

func fetchProjectOverviewForTest(
	t *testing.T,
	client *http.Client,
	baseURL string,
	projectID string,
) projectOverviewResponseForTest {
	t.Helper()

	resp, err := client.Get(baseURL + "/api/projects/" + projectID + "/overview")
	if err != nil {
		t.Fatalf("request project overview: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%q", resp.StatusCode, string(payload))
	}

	var body projectOverviewResponseForTest
	decodeErr := json.NewDecoder(resp.Body).Decode(&body)
	if decodeErr != nil {
		t.Fatalf("decode overview response: %v", decodeErr)
	}
	return body
}

func assertProjectOverviewReadModelForTest(t *testing.T, overview projectOverviewForTest) {
	t.Helper()

	if strings.TrimSpace(overview.Summary) == "" {
		t.Fatal("expected overview summary to be populated")
	}

	gotOrder := make([]string, 0, len(overview.Environments))
	for _, env := range overview.Environments {
		gotOrder = append(gotOrder, env.Name)
	}
	wantOrder := []string{"dev", "staging", "qa", "prod"}
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("unexpected overview environment count: got %d want %d", len(gotOrder), len(wantOrder))
	}
	for idx := range wantOrder {
		if gotOrder[idx] != wantOrder[idx] {
			t.Fatalf("unexpected environment order at %d: got %q want %q", idx, gotOrder[idx], wantOrder[idx])
		}
	}

	if got := overview.Environments[0].DeliveryType; got != "deploy" {
		t.Fatalf("expected dev delivery_type deploy, got %q", got)
	}
	if got := overview.Environments[1].DeliveryType; got != "promote" {
		t.Fatalf("expected staging delivery_type promote, got %q", got)
	}
	if got := overview.Environments[3].DeliveryType; got != "none" {
		t.Fatalf("expected prod delivery_type none, got %q", got)
	}
	if got := overview.Environments[1].ConfigReadiness; got != "ok" {
		t.Fatalf("expected staging config_readiness ok, got %q", got)
	}
	if got := overview.Environments[0].SecretsReadiness; got != "unsupported" {
		t.Fatalf("expected secrets_readiness unsupported, got %q", got)
	}
	if strings.TrimSpace(overview.Environments[1].LastDeliveryAt) == "" {
		t.Fatal("expected last_delivery_at for staging")
	}
}

func assertOverviewExcludesRawEnvValuesForTest(
	t *testing.T,
	overview projectOverviewForTest,
	secretValue string,
) {
	t.Helper()
	overviewJSON, marshalErr := json.Marshal(overview)
	if marshalErr != nil {
		t.Fatalf("marshal overview for secret assertion: %v", marshalErr)
	}
	if strings.Contains(string(overviewJSON), secretValue) {
		t.Fatal("overview payload contains raw environment variable values")
	}
}
