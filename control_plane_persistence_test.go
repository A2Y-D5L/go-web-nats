//nolint:testpackage // Persistence and restart tests exercise unexported runtime wiring.
package platform

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

type persistentNATSFixture struct {
	t        *testing.T
	storeDir string
	ns       any
	nc       *nats.Conn
	js       jetstream.JetStream
	store    *Store
}

func startPersistentNATSFixture(t *testing.T, storeDir string) *persistentNATSFixture {
	t.Helper()
	t.Setenv(natsStoreDirEnv, storeDir)

	ns, natsURL, resolvedDir, isEphemeral, err := startEmbeddedNATS()
	if err != nil {
		t.Skipf("embedded nats is unavailable in this environment: %v", err)
	}
	if isEphemeral {
		ns.Shutdown()
		ns.WaitForShutdown()
		t.Fatalf("expected persistent nats store mode, got ephemeral")
	}
	if resolvedDir != storeDir {
		ns.Shutdown()
		ns.WaitForShutdown()
		t.Fatalf("expected nats store dir %q, got %q", storeDir, resolvedDir)
	}

	nc, err := nats.Connect(natsURL, nats.Name("control-plane-persistence-test"))
	if err != nil {
		ns.Shutdown()
		ns.WaitForShutdown()
		t.Skipf("nats connect is unavailable in this environment: %v", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		_ = nc.Drain()
		ns.Shutdown()
		ns.WaitForShutdown()
		t.Skipf("jetstream client is unavailable in this environment: %v", err)
	}
	store, err := newStore(context.Background(), js)
	if err != nil {
		_ = nc.Drain()
		ns.Shutdown()
		ns.WaitForShutdown()
		t.Skipf("store initialization is unavailable in this environment: %v", err)
	}
	return &persistentNATSFixture{
		t:        t,
		storeDir: resolvedDir,
		ns:       ns,
		nc:       nc,
		js:       js,
		store:    store,
	}
}

func (f *persistentNATSFixture) close() {
	if f == nil {
		return
	}
	t := f.t
	t.Helper()
	if f.nc != nil {
		if err := f.nc.Drain(); err != nil {
			t.Fatalf("drain nats connection: %v", err)
		}
	}
	if ns, ok := f.ns.(interface {
		Shutdown()
		WaitForShutdown()
	}); ok {
		ns.Shutdown()
		ns.WaitForShutdown()
	}
}

func readOneSSEEvent(t *testing.T, body io.Reader) (string, string, error) {
	t.Helper()
	reader := bufio.NewReader(body)
	eventName := ""
	data := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return eventName, data, nil
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		field := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch field {
		case "event":
			eventName = value
		case "data":
			if data == "" {
				data = value
			} else {
				data += "\n" + value
			}
		}
	}
}

func requireLoopbackListenCapability(t *testing.T) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("loopback listener is unavailable in this environment: %v", err)
		return
	}
	_ = listener.Close()
}

func TestControlPlaneStatePersistsAcrossNATSRestart(t *testing.T) {
	requireLoopbackListenCapability(t)

	storeDir := t.TempDir() + "/nats-store"
	fixtureOne := startPersistentNATSFixture(t, storeDir)

	now := time.Now().UTC()
	project := Project{
		ID:        "project-persist-1",
		CreatedAt: now,
		UpdatedAt: now,
		Spec: normalizeProjectSpec(ProjectSpec{
			APIVersion: projectAPIVersion,
			Kind:       projectKind,
			Name:       "persist-app",
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
		}),
		Status: ProjectStatus{
			Phase:      projectPhaseReady,
			UpdatedAt:  now,
			LastOpID:   "op-persist-1",
			LastOpKind: string(OpCreate),
			Message:    "ready",
		},
	}
	op := Operation{
		ID:        "op-persist-1",
		Kind:      OpCreate,
		ProjectID: project.ID,
		Delivery: DeliveryLifecycle{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
		Requested: now,
		Finished:  time.Time{},
		Status:    opStatusRunning,
		Error:     "",
		Steps: []OpStep{
			{
				Worker:    "registrar",
				StartedAt: now.Add(2 * time.Second),
				EndedAt:   time.Time{},
				Message:   "registering",
				Error:     "",
				Artifacts: nil,
			},
		},
	}

	if err := fixtureOne.store.PutProject(context.Background(), project); err != nil {
		fixtureOne.close()
		t.Fatalf("put project: %v", err)
	}
	if err := fixtureOne.store.PutOp(context.Background(), op); err != nil {
		fixtureOne.close()
		t.Fatalf("put operation: %v", err)
	}
	fixtureOne.close()

	fixtureTwo := startPersistentNATSFixture(t, storeDir)
	defer fixtureTwo.close()

	gotProject, err := fixtureTwo.store.GetProject(context.Background(), project.ID)
	if err != nil {
		t.Fatalf("get project after restart: %v", err)
	}
	if gotProject.ID != project.ID {
		t.Fatalf("expected project id %q, got %q", project.ID, gotProject.ID)
	}
	gotOp, err := fixtureTwo.store.GetOp(context.Background(), op.ID)
	if err != nil {
		t.Fatalf("get operation after restart: %v", err)
	}
	if gotOp.ID != op.ID {
		t.Fatalf("expected op id %q, got %q", op.ID, gotOp.ID)
	}
	if gotOp.Status != opStatusRunning {
		t.Fatalf("expected op status %q, got %q", opStatusRunning, gotOp.Status)
	}
}

func TestOpEventsBootstrapRebuildsSnapshotAfterRestartWithoutHistory(t *testing.T) {
	requireLoopbackListenCapability(t)

	storeDir := t.TempDir() + "/nats-store"
	fixtureOne := startPersistentNATSFixture(t, storeDir)
	now := time.Now().UTC()
	stepStarted := now.Add(5 * time.Second)
	stepEnded := stepStarted.Add(4 * time.Second)
	op := Operation{
		ID:        "op-restart-bootstrap-1",
		Kind:      OpDeploy,
		ProjectID: "project-restart-bootstrap-1",
		Delivery: DeliveryLifecycle{
			Stage:       DeliveryStageDeploy,
			Environment: "dev",
			FromEnv:     "",
			ToEnv:       "",
		},
		Requested: now,
		Finished:  stepEnded.Add(2 * time.Second),
		Status:    opStatusError,
		Error:     "no build image found for deployment",
		Steps: []OpStep{
			{
				Worker:    "manifestRenderer",
				StartedAt: stepStarted,
				EndedAt:   stepEnded,
				Message:   "",
				Error:     "",
				Artifacts: []string{"deploy/dev/rendered.yaml"},
			},
		},
	}
	if err := fixtureOne.store.PutOp(context.Background(), op); err != nil {
		fixtureOne.close()
		t.Fatalf("put operation: %v", err)
	}
	fixtureOne.close()

	fixtureTwo := startPersistentNATSFixture(t, storeDir)
	defer fixtureTwo.close()

	hub := newOpEventHub(opEventsHistoryLimit, opEventsRetention)
	fixtureTwo.store.setOpEvents(hub)
	api := &API{
		nc:                   fixtureTwo.nc,
		store:                fixtureTwo.store,
		artifacts:            NewFSArtifacts(t.TempDir()),
		waiters:              newWaiterHub(),
		opEvents:             hub,
		opHeartbeatInterval:  5 * time.Second,
		runtimeVersion:       "",
		runtimeHTTPAddr:      httpAddr,
		runtimeArtifactsRoot: "",
		runtimeBuilderMode: imageBuilderModeResolution{
			requestedMode:     imageBuilderModeBuildKit,
			requestedExplicit: false,
			effectiveMode:     imageBuilderModeBuildKit,
			requestedWarning:  "",
			fallbackReason:    "",
			policyError:       "",
		},
		runtimeCommitWatcherEnabled: false,
		runtimeNATSEmbedded:         true,
		runtimeNATSStoreDir:         fixtureTwo.storeDir,
		runtimeNATSStoreEphemeral:   false,
		sourceTriggerMu:             sync.Mutex{},
		projectStartLocksMu:         sync.Mutex{},
		projectStartLocks:           map[string]*sync.Mutex{},
	}

	srv := httptest.NewServer(api.routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/ops/" + op.ID + "/events")
	if err != nil {
		t.Fatalf("open sse stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 status, got %d", resp.StatusCode)
	}

	eventName, data, err := readOneSSEEvent(t, resp.Body)
	if err != nil {
		t.Fatalf("read first sse event: %v", err)
	}
	if eventName != opEventBootstrap {
		t.Fatalf("expected first event %q, got %q", opEventBootstrap, eventName)
	}
	var payload opEventPayload
	unmarshalErr := json.Unmarshal([]byte(data), &payload)
	if unmarshalErr != nil {
		t.Fatalf("decode bootstrap payload: %v", unmarshalErr)
	}
	if payload.OpID != op.ID {
		t.Fatalf("expected op id %q, got %q", op.ID, payload.OpID)
	}
	if payload.Status != opStatusError {
		t.Fatalf("expected status %q, got %q", opStatusError, payload.Status)
	}
	if payload.Worker != "manifestRenderer" {
		t.Fatalf("expected worker manifestRenderer, got %q", payload.Worker)
	}
	if payload.StepIndex != 1 {
		t.Fatalf("expected step index 1, got %d", payload.StepIndex)
	}
	if payload.Error != op.Error {
		t.Fatalf("expected error %q, got %q", op.Error, payload.Error)
	}
	if payload.Hint == "" {
		t.Fatal("expected hint in bootstrap payload for failure status")
	}
}
