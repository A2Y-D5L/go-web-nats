//go:build integration
// +build integration

//nolint:testpackage // Integration-style tests need access to internal runtime helpers.
package platform

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type asyncAPIFixture struct {
	api      *API
	nc       *nats.Conn
	nsDir    string
	nsCancel func()
}

func newAsyncAPIFixture(t *testing.T, heartbeat time.Duration) *asyncAPIFixture {
	t.Helper()

	ctx := context.Background()
	ns, natsURL, nsDir, err := startEmbeddedNATS()
	if err != nil {
		t.Skipf("embedded nats is unavailable in this environment: %v", err)
	}

	nc, err := nats.Connect(natsURL, nats.Name("api-async-test"))
	if err != nil {
		ns.Shutdown()
		ns.WaitForShutdown()
		_ = os.RemoveAll(nsDir)
		t.Skipf("nats connect unavailable in this environment: %v", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		_ = nc.Drain()
		ns.Shutdown()
		ns.WaitForShutdown()
		_ = os.RemoveAll(nsDir)
		t.Skipf("jetstream setup unavailable in this environment: %v", err)
	}

	store, err := newStore(ctx, js)
	if err != nil {
		_ = nc.Drain()
		ns.Shutdown()
		ns.WaitForShutdown()
		_ = os.RemoveAll(nsDir)
		t.Skipf("store setup unavailable in this environment: %v", err)
	}

	hub := newOpEventHub(opEventsHistoryLimit, opEventsRetention)
	store.setOpEvents(hub)

	api := &API{
		nc:                  nc,
		store:               store,
		artifacts:           NewFSArtifacts(t.TempDir()),
		waiters:             newWaiterHub(),
		opEvents:            hub,
		opHeartbeatInterval: heartbeat,
		sourceTriggerMu:     sync.Mutex{},
	}

	cleanup := func() {
		_ = nc.Drain()
		ns.Shutdown()
		ns.WaitForShutdown()
		_ = os.RemoveAll(nsDir)
	}

	return &asyncAPIFixture{
		api:      api,
		nc:       nc,
		nsDir:    nsDir,
		nsCancel: cleanup,
	}
}

func (f *asyncAPIFixture) Close() {
	if f == nil || f.nsCancel == nil {
		return
	}
	f.nsCancel()
}

func testProjectSpec(name string) ProjectSpec {
	return normalizeProjectSpec(ProjectSpec{
		APIVersion: projectAPIVersion,
		Kind:       projectKind,
		Name:       name,
		Runtime:    "go_1.26",
		Capabilities: []string{
			"http",
		},
		Environments: map[string]EnvConfig{
			"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
		},
		NetworkPolicies: NetworkPolicies{
			Ingress: networkPolicyInternal,
			Egress:  networkPolicyInternal,
		},
	})
}

func TestAPI_RegistrationCreateReturnsAcceptedAndQueuedOp(t *testing.T) {
	fixture := newAsyncAPIFixture(t, opEventsHeartbeatInterval)
	defer fixture.Close()

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	body, err := json.Marshal(map[string]any{
		"action": "create",
		"spec":   testProjectSpec("async-create"),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	started := time.Now()
	resp, err := http.Post(srv.URL+"/api/events/registration", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request create registration event: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 202 Accepted, got %d body=%q", resp.StatusCode, string(payload))
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("expected async response within 2s, got %s", elapsed)
	}

	var out struct {
		Accepted bool      `json:"accepted"`
		Project  Project   `json:"project"`
		Op       Operation `json:"op"`
	}
	decodeErr := json.NewDecoder(resp.Body).Decode(&out)
	if decodeErr != nil {
		t.Fatalf("decode response: %v", decodeErr)
	}
	if !out.Accepted {
		t.Fatal("expected accepted=true")
	}
	if strings.TrimSpace(out.Op.ID) == "" {
		t.Fatal("expected op.id in async response")
	}
	if out.Op.Kind != OpCreate {
		t.Fatalf("expected op kind %q, got %q", OpCreate, out.Op.Kind)
	}
	if out.Op.Status != statusMessageQueued {
		t.Fatalf("expected op status %q, got %q", statusMessageQueued, out.Op.Status)
	}
	stored, err := fixture.api.store.GetOp(context.Background(), out.Op.ID)
	if err != nil {
		t.Fatalf("read stored op: %v", err)
	}
	if stored.Status != statusMessageQueued {
		t.Fatalf("expected stored op status %q, got %q", statusMessageQueued, stored.Status)
	}
}

type sseEvent struct {
	id    string
	event string
	data  string
}

func readNextSSEEvent(reader *bufio.Reader) (sseEvent, error) {
	ev := sseEvent{id: "", event: "", data: ""}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return sseEvent{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if ev.event == "" && ev.data == "" && ev.id == "" {
				continue
			}
			return ev, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		field := strings.TrimSpace(parts[0])
		value := ""
		if len(parts) == 2 {
			value = strings.TrimSpace(parts[1])
		}
		switch field {
		case "id":
			ev.id = value
		case "event":
			ev.event = value
		case "data":
			if ev.data == "" {
				ev.data = value
			} else {
				ev.data += "\n" + value
			}
		}
	}
}

func waitForSSEEvent(
	t *testing.T,
	events <-chan sseEvent,
	errCh <-chan error,
	name string,
	timeout time.Duration,
) sseEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case err := <-errCh:
			if err == nil {
				t.Fatalf("sse stream closed unexpectedly while waiting for %s", name)
			}
			if errors.Is(err, io.EOF) {
				t.Fatalf("sse stream ended before %s", name)
			}
			t.Fatalf("read sse event: %v", err)
		case ev := <-events:
			if ev.event == name {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for sse event %s", name)
		}
	}
}

func TestAPI_OpEventsStreamsBootstrapStepAndHeartbeat(t *testing.T) {
	fixture := newAsyncAPIFixture(t, 40*time.Millisecond)
	defer fixture.Close()

	op := Operation{
		ID:        "op-stream-1",
		Kind:      OpCreate,
		ProjectID: "project-stream",
		Delivery: DeliveryLifecycle{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
		Requested: time.Now().UTC(),
		Finished:  time.Time{},
		Status:    statusMessageQueued,
		Error:     "",
		Steps:     []OpStep{},
	}
	if err := fixture.api.store.PutOp(context.Background(), op); err != nil {
		t.Fatalf("put op fixture: %v", err)
	}
	emitOpBootstrap(fixture.api.opEvents, op, "operation accepted and queued")

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/ops/"+op.ID+"/events", nil)
	if err != nil {
		t.Fatalf("build events request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stream op events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%q", resp.StatusCode, string(payload))
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("expected text/event-stream, got %q", got)
	}

	reader := bufio.NewReader(resp.Body)
	events := make(chan sseEvent, 32)
	errCh := make(chan error, 1)
	go func() {
		for {
			eventItem, readErr := readNextSSEEvent(reader)
			if readErr != nil {
				errCh <- readErr
				return
			}
			events <- eventItem
		}
	}()

	bootstrap := waitForSSEEvent(t, events, errCh, opEventBootstrap, 2*time.Second)
	var bootstrapPayload opEventPayload
	decodeBootstrapErr := json.Unmarshal([]byte(bootstrap.data), &bootstrapPayload)
	if decodeBootstrapErr != nil {
		t.Fatalf("decode bootstrap payload: %v", decodeBootstrapErr)
	}
	if bootstrapPayload.OpID != op.ID {
		t.Fatalf("expected bootstrap op id %q, got %q", op.ID, bootstrapPayload.OpID)
	}

	op.Status = "running"
	op.Steps = append(op.Steps, OpStep{
		Worker:    "registrar",
		StartedAt: time.Now().UTC(),
		EndedAt:   time.Time{},
		Message:   "register app configuration",
		Error:     "",
		Artifacts: nil,
	})
	emitOpStepStarted(fixture.api.opEvents, op, "registrar", 1, "register app configuration")

	started := waitForSSEEvent(t, events, errCh, opEventStarted, 2*time.Second)
	var startedPayload opEventPayload
	decodeStartedErr := json.Unmarshal([]byte(started.data), &startedPayload)
	if decodeStartedErr != nil {
		t.Fatalf("decode step.started payload: %v", decodeStartedErr)
	}
	if startedPayload.Worker != "registrar" {
		t.Fatalf("expected worker registrar, got %q", startedPayload.Worker)
	}

	heartbeat := waitForSSEEvent(t, events, errCh, opEventHeartbeat, 2*time.Second)
	if heartbeat.id != "" {
		t.Fatalf("expected heartbeat protocol id to be omitted, got %q", heartbeat.id)
	}
}
