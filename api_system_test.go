package platform_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	platform "github.com/a2y-d5l/go-web-nats"
)

type systemStatusPayload struct {
	Version              string `json:"version"`
	HTTPAddr             string `json:"http_addr"`
	ArtifactsRoot        string `json:"artifacts_root"`
	BuilderModeRequested string `json:"builder_mode_requested"`
	BuilderModeEffective string `json:"builder_mode_effective"`
	BuilderModeReason    string `json:"builder_mode_reason"`
	CommitWatcherEnabled bool   `json:"commit_watcher_enabled"`
	NATS                 struct {
		Embedded     bool   `json:"embedded"`
		StoreDirMode string `json:"store_dir_mode"`
	} `json:"nats"`
	Realtime struct {
		SSEEnabled           bool   `json:"sse_enabled"`
		SSEReplayWindow      int    `json:"sse_replay_window"`
		SSEHeartbeatInterval string `json:"sse_heartbeat_interval"`
	} `json:"realtime"`
	Time time.Time `json:"time"`
}

func TestAPIHandleSystemReturnsRuntimeCapabilities(t *testing.T) {
	api := platform.NewTestAPI(newMemArtifacts())
	platform.ConfigureRuntimeSystemForTest(api, platform.RuntimeSystemConfigForTest{
		Version:              "v1.2.3-test",
		HTTPAddr:             "127.0.0.1:8080",
		ArtifactsRoot:        "/tmp/platform-artifacts",
		BuilderModeRequested: "buildkit",
		BuilderModeEffective: "artifact",
		BuilderModeReason:    "buildkit support is unavailable in this binary",
		CommitWatcherEnabled: true,
		NATSEmbedded:         true,
		NATSStoreDir:         "/tmp/nats",
		NATSStoreEphemeral:   true,
		SSEEnabled:           true,
		SSEReplayWindow:      321,
		SSEHeartbeatInterval: 7 * time.Second,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/system", nil)
	rec := httptest.NewRecorder()
	platform.InvokeHandleSystemForTest(api, rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rec.Code, rec.Body.String())
	}

	var payload systemStatusPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if payload.Version != "v1.2.3-test" {
		t.Fatalf("version = %q, want %q", payload.Version, "v1.2.3-test")
	}
	if payload.HTTPAddr != "127.0.0.1:8080" {
		t.Fatalf("http_addr = %q", payload.HTTPAddr)
	}
	if payload.ArtifactsRoot != "/tmp/platform-artifacts" {
		t.Fatalf("artifacts_root = %q", payload.ArtifactsRoot)
	}
	if payload.BuilderModeRequested != "buildkit" {
		t.Fatalf("builder_mode_requested = %q", payload.BuilderModeRequested)
	}
	if payload.BuilderModeEffective != "artifact" {
		t.Fatalf("builder_mode_effective = %q", payload.BuilderModeEffective)
	}
	if payload.BuilderModeReason == "" {
		t.Fatal("builder_mode_reason should be populated when mode is downgraded")
	}
	if !payload.CommitWatcherEnabled {
		t.Fatal("commit_watcher_enabled should be true")
	}
	if !payload.NATS.Embedded {
		t.Fatal("nats.embedded should be true")
	}
	if payload.NATS.StoreDirMode != "ephemeral" {
		t.Fatalf("nats.store_dir_mode = %q", payload.NATS.StoreDirMode)
	}
	if payload.Realtime.SSEReplayWindow != 321 {
		t.Fatalf("realtime.sse_replay_window = %d", payload.Realtime.SSEReplayWindow)
	}
	if payload.Realtime.SSEHeartbeatInterval != "7s" {
		t.Fatalf("realtime.sse_heartbeat_interval = %q", payload.Realtime.SSEHeartbeatInterval)
	}
	if !payload.Realtime.SSEEnabled {
		t.Fatal("realtime.sse_enabled should be true when op events are available")
	}
	if payload.Time.IsZero() {
		t.Fatal("time should be set")
	}
}

func TestAPIRoutesSystemAndHealthz(t *testing.T) {
	api := platform.NewTestAPI(newMemArtifacts())
	platform.ConfigureRuntimeSystemForTest(api, platform.RuntimeSystemConfigForTest{
		Version:              "",
		HTTPAddr:             "127.0.0.1:8080",
		ArtifactsRoot:        "",
		BuilderModeRequested: "buildkit",
		BuilderModeEffective: "buildkit",
		BuilderModeReason:    "",
		CommitWatcherEnabled: false,
		NATSEmbedded:         false,
		NATSStoreDir:         "",
		NATSStoreEphemeral:   false,
		SSEEnabled:           false,
		SSEReplayWindow:      0,
		SSEHeartbeatInterval: 0,
	})
	handler := platform.RoutesForTest(api)

	recSystem := httptest.NewRecorder()
	reqSystem := httptest.NewRequest(http.MethodGet, "/api/system", nil)
	handler.ServeHTTP(recSystem, reqSystem)
	if recSystem.Code != http.StatusOK {
		t.Fatalf("expected /api/system status 200, got %d", recSystem.Code)
	}

	recHealth := httptest.NewRecorder()
	reqHealth := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	handler.ServeHTTP(recHealth, reqHealth)
	if recHealth.Code != http.StatusOK {
		t.Fatalf("expected /api/healthz status 200, got %d", recHealth.Code)
	}
}

func TestAPIHandleSystemRejectsUnsupportedMethod(t *testing.T) {
	api := platform.NewTestAPI(newMemArtifacts())
	req := httptest.NewRequest(http.MethodPost, "/api/system", nil)
	rec := httptest.NewRecorder()

	platform.InvokeHandleSystemForTest(api, rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}
