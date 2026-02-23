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
	nsDirTmp bool
	nsCancel func()
}

func newAsyncAPIFixture(t *testing.T, heartbeat time.Duration) *asyncAPIFixture {
	t.Helper()

	ctx := context.Background()
	t.Setenv(natsStoreDirEnv, natsStoreDirModeTemp)
	ns, natsURL, nsDir, nsDirTmp, err := startEmbeddedNATS()
	if err != nil {
		t.Skipf("embedded nats is unavailable in this environment: %v", err)
	}

	nc, err := nats.Connect(natsURL, nats.Name("api-async-test"))
	if err != nil {
		ns.Shutdown()
		ns.WaitForShutdown()
		if nsDirTmp {
			_ = os.RemoveAll(nsDir)
		}
		t.Skipf("nats connect unavailable in this environment: %v", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		_ = nc.Drain()
		ns.Shutdown()
		ns.WaitForShutdown()
		if nsDirTmp {
			_ = os.RemoveAll(nsDir)
		}
		t.Skipf("jetstream setup unavailable in this environment: %v", err)
	}

	store, err := newStore(ctx, js)
	if err != nil {
		_ = nc.Drain()
		ns.Shutdown()
		ns.WaitForShutdown()
		if nsDirTmp {
			_ = os.RemoveAll(nsDir)
		}
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
		projectStartLocksMu: sync.Mutex{},
		projectStartLocks:   map[string]*sync.Mutex{},
	}

	cleanup := func() {
		_ = nc.Drain()
		ns.Shutdown()
		ns.WaitForShutdown()
		if nsDirTmp {
			_ = os.RemoveAll(nsDir)
		}
	}

	return &asyncAPIFixture{
		api:      api,
		nc:       nc,
		nsDir:    nsDir,
		nsDirTmp: nsDirTmp,
		nsCancel: cleanup,
	}
}

func (f *asyncAPIFixture) Close() {
	if f == nil || f.nsCancel == nil {
		return
	}
	f.nsCancel()
}

func makeClosedPublishConn(t *testing.T, fixture *asyncAPIFixture, name string) *nats.Conn {
	t.Helper()
	if fixture == nil || fixture.nc == nil {
		t.Fatal("fixture nats connection is unavailable")
	}
	url := strings.TrimSpace(fixture.nc.ConnectedUrl())
	if url == "" {
		t.Fatal("fixture nats connected url is empty")
	}
	nc, err := nats.Connect(url, nats.Name(name))
	if err != nil {
		t.Fatalf("connect closed publish fixture: %v", err)
	}
	nc.Close()
	return nc
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

func putProjectFixture(
	t *testing.T,
	fixture *asyncAPIFixture,
	projectID string,
	spec ProjectSpec,
	lastOpID string,
	lastOpKind OperationKind,
) {
	t.Helper()
	now := time.Now().UTC()
	project := Project{
		ID:        projectID,
		CreatedAt: now,
		UpdatedAt: now,
		Spec:      spec,
		Status: ProjectStatus{
			Phase:      "Reconciling",
			UpdatedAt:  now,
			LastOpID:   lastOpID,
			LastOpKind: string(lastOpKind),
			Message:    "running",
		},
	}
	if err := fixture.api.store.PutProject(context.Background(), project); err != nil {
		t.Fatalf("put project fixture: %v", err)
	}
}

func putOpFixture(
	t *testing.T,
	fixture *asyncAPIFixture,
	opID string,
	projectID string,
	kind OperationKind,
	status string,
) {
	t.Helper()
	op := Operation{
		ID:        opID,
		Kind:      kind,
		ProjectID: projectID,
		Delivery: DeliveryLifecycle{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
		Requested: time.Now().UTC(),
		Finished:  time.Time{},
		Status:    status,
		Error:     "",
		Steps:     []OpStep{},
	}
	if status == opStatusDone || status == opStatusError {
		op.Finished = time.Now().UTC()
	}
	if err := fixture.api.store.PutOp(context.Background(), op); err != nil {
		t.Fatalf("put op fixture: %v", err)
	}
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

func TestAPI_RegistrationCreatePublishFailureReturnsRecoveryMetadataAndRollsBackProject(t *testing.T) {
	fixture := newAsyncAPIFixture(t, opEventsHeartbeatInterval)
	defer fixture.Close()

	fixture.api.nc = makeClosedPublishConn(t, fixture, "api-async-test-publish-fail-registration")

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	body, err := json.Marshal(map[string]any{
		"action": "create",
		"spec":   testProjectSpec("create-publish-fail-registration"),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	resp, err := http.Post(srv.URL+"/api/events/registration", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request create registration event: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 500, got %d body=%q", resp.StatusCode, string(payload))
	}

	var out struct {
		Accepted          bool          `json:"accepted"`
		Reason            string        `json:"reason"`
		ProjectID         string        `json:"project_id"`
		RequestedKind     OperationKind `json:"requested_kind"`
		OpID              string        `json:"op_id"`
		ProjectRolledBack bool          `json:"project_rolled_back"`
		NextStep          string        `json:"next_step"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&out); decodeErr != nil {
		t.Fatalf("decode response: %v", decodeErr)
	}
	if out.Accepted {
		t.Fatal("expected accepted=false")
	}
	if strings.TrimSpace(out.ProjectID) == "" {
		t.Fatal("expected project_id in failure response")
	}
	if strings.TrimSpace(out.OpID) == "" {
		t.Fatal("expected op_id in failure response")
	}
	if out.RequestedKind != OpCreate {
		t.Fatalf("expected requested_kind=%q, got %q", OpCreate, out.RequestedKind)
	}
	if strings.TrimSpace(out.Reason) == "" {
		t.Fatal("expected reason in failure response")
	}
	if strings.TrimSpace(out.NextStep) == "" {
		t.Fatal("expected next_step in failure response")
	}
	if !out.ProjectRolledBack {
		t.Fatal("expected project_rolled_back=true after create enqueue failure")
	}

	if _, getErr := fixture.api.store.GetProject(context.Background(), out.ProjectID); !errors.Is(getErr, jetstream.ErrKeyNotFound) {
		t.Fatalf("expected rolled-back project to be absent, got err=%v", getErr)
	}
	op, getErr := fixture.api.store.GetOp(context.Background(), out.OpID)
	if getErr != nil {
		t.Fatalf("read failed op: %v", getErr)
	}
	if op.Status != opStatusError {
		t.Fatalf("expected failed op status %q, got %q", opStatusError, op.Status)
	}
}

func TestAPI_ProjectsCreatePublishFailureReturnsRecoveryMetadataAndRollsBackProject(t *testing.T) {
	fixture := newAsyncAPIFixture(t, opEventsHeartbeatInterval)
	defer fixture.Close()

	fixture.api.nc = makeClosedPublishConn(t, fixture, "api-async-test-publish-fail-projects")

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	body, err := json.Marshal(testProjectSpec("create-publish-fail-projects"))
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	resp, err := http.Post(srv.URL+"/api/projects", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request project create: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 500, got %d body=%q", resp.StatusCode, string(payload))
	}

	var out struct {
		Accepted          bool          `json:"accepted"`
		ProjectID         string        `json:"project_id"`
		RequestedKind     OperationKind `json:"requested_kind"`
		OpID              string        `json:"op_id"`
		ProjectRolledBack bool          `json:"project_rolled_back"`
		NextStep          string        `json:"next_step"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&out); decodeErr != nil {
		t.Fatalf("decode response: %v", decodeErr)
	}
	if out.Accepted {
		t.Fatal("expected accepted=false")
	}
	if out.RequestedKind != OpCreate {
		t.Fatalf("expected requested_kind=%q, got %q", OpCreate, out.RequestedKind)
	}
	if strings.TrimSpace(out.ProjectID) == "" || strings.TrimSpace(out.OpID) == "" {
		t.Fatalf("expected project_id and op_id in response, got project=%q op=%q", out.ProjectID, out.OpID)
	}
	if strings.TrimSpace(out.NextStep) == "" {
		t.Fatal("expected next_step in failure response")
	}
	if !out.ProjectRolledBack {
		t.Fatal("expected project_rolled_back=true after create enqueue failure")
	}
	if _, getErr := fixture.api.store.GetProject(context.Background(), out.ProjectID); !errors.Is(getErr, jetstream.ErrKeyNotFound) {
		t.Fatalf("expected rolled-back project to be absent, got err=%v", getErr)
	}
}

func TestAPI_EnqueuePublishFailureDoesNotEmitQueuedSignals(t *testing.T) {
	fixture := newAsyncAPIFixture(t, opEventsHeartbeatInterval)
	defer fixture.Close()

	projectID := "project-enqueue-publish-fail-no-queued-events"
	spec := testProjectSpec("enqueue-publish-fail-no-queued-events")
	putProjectFixture(t, fixture, projectID, spec, "", "")
	fixture.api.nc = makeClosedPublishConn(t, fixture, "api-async-test-publish-fail-events")

	_, err := fixture.api.enqueueOp(context.Background(), OpUpdate, projectID, spec, emptyOpRunOptions())
	if err == nil {
		t.Fatal("expected enqueue publish failure")
	}

	var enqueueErr *opEnqueueError
	if !errors.As(err, &enqueueErr) {
		t.Fatalf("expected *opEnqueueError, got %T", err)
	}
	if strings.TrimSpace(enqueueErr.OpID) == "" {
		t.Fatal("expected op id in enqueue error")
	}
	if enqueueErr.RequestedKind != OpUpdate {
		t.Fatalf("expected requested kind %q, got %q", OpUpdate, enqueueErr.RequestedKind)
	}

	replay, _, needsBootstrap, unsubscribe := fixture.api.opEvents.subscribe(enqueueErr.OpID, "0")
	defer unsubscribe()
	if needsBootstrap {
		t.Fatal("expected replay without bootstrap for last_event_id=0")
	}
	if len(replay) == 0 {
		t.Fatal("expected replay records for failed enqueue op")
	}

	foundBootstrap := false
	foundQueuedStatus := false
	foundErrorSignal := false
	for _, record := range replay {
		if record.Name == opEventBootstrap {
			foundBootstrap = true
		}
		if record.Name == opEventStatus && record.Payload.Status == statusMessageQueued {
			foundQueuedStatus = true
		}
		if (record.Name == opEventStatus && record.Payload.Status == opStatusError) || record.Name == opEventFailed {
			foundErrorSignal = true
		}
	}
	if foundBootstrap {
		t.Fatal("did not expect op.bootstrap on publish failure")
	}
	if foundQueuedStatus {
		t.Fatal("did not expect queued op.status before publish success")
	}
	if !foundErrorSignal {
		t.Fatal("expected error terminal signal for failed enqueue")
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

func TestAPI_RegistrationUpdateRejectsWhenProjectHasActiveOperation(t *testing.T) {
	fixture := newAsyncAPIFixture(t, opEventsHeartbeatInterval)
	defer fixture.Close()

	projectID := "project-ops-conflict-update"
	activeOpID := "op-running-update"
	initialSpec := testProjectSpec("update-conflict-original")
	putProjectFixture(t, fixture, projectID, initialSpec, activeOpID, OpDeploy)
	putOpFixture(t, fixture, activeOpID, projectID, OpDeploy, opStatusRunning)

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	body, err := json.Marshal(map[string]any{
		"action":     "update",
		"project_id": projectID,
		"spec":       testProjectSpec("update-conflict-next"),
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(srv.URL+"/api/events/registration", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request registration update: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 409 conflict, got %d body=%q", resp.StatusCode, string(payload))
	}

	var out struct {
		Accepted bool   `json:"accepted"`
		Reason   string `json:"reason"`
		ActiveOp struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Status string `json:"status"`
		} `json:"active_op"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&out); decodeErr != nil {
		t.Fatalf("decode conflict response: %v", decodeErr)
	}
	if out.Accepted {
		t.Fatal("expected accepted=false on conflict response")
	}
	if !strings.Contains(strings.ToLower(out.Reason), "active operation") {
		t.Fatalf("expected conflict reason to mention active operation, got %q", out.Reason)
	}
	if out.ActiveOp.ID != activeOpID {
		t.Fatalf("expected active op id %q, got %q", activeOpID, out.ActiveOp.ID)
	}

	storedProject, err := fixture.api.store.GetProject(context.Background(), projectID)
	if err != nil {
		t.Fatalf("read project after rejected update: %v", err)
	}
	if storedProject.Spec.Name != initialSpec.Name {
		t.Fatalf("expected project spec to remain %q, got %q", initialSpec.Name, storedProject.Spec.Name)
	}
}

func TestAPI_SourceWebhookConflictRollsBackPendingCommitAndAllowsRetry(t *testing.T) {
	fixture := newAsyncAPIFixture(t, opEventsHeartbeatInterval)
	defer fixture.Close()

	projectID := "project-ops-conflict-webhook"
	activeOpID := "op-running-webhook"
	spec := testProjectSpec("webhook-conflict")
	putProjectFixture(t, fixture, projectID, spec, activeOpID, OpDeploy)
	putOpFixture(t, fixture, activeOpID, projectID, OpDeploy, opStatusRunning)

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	webhookPayload, err := json.Marshal(map[string]any{
		"project_id": projectID,
		"repo":       "source",
		"branch":     "main",
		"commit":     "abc123",
	})
	if err != nil {
		t.Fatalf("marshal webhook payload: %v", err)
	}

	resp, err := http.Post(srv.URL+"/api/webhooks/source", "application/json", bytes.NewReader(webhookPayload))
	if err != nil {
		t.Fatalf("send webhook conflict request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 409 conflict, got %d body=%q", resp.StatusCode, string(payload))
	}

	putOpFixture(t, fixture, activeOpID, projectID, OpDeploy, opStatusDone)

	retryResp, err := http.Post(srv.URL+"/api/webhooks/source", "application/json", bytes.NewReader(webhookPayload))
	if err != nil {
		t.Fatalf("send webhook retry request: %v", err)
	}
	defer retryResp.Body.Close()
	if retryResp.StatusCode != http.StatusAccepted {
		payload, _ := io.ReadAll(retryResp.Body)
		t.Fatalf("expected 202 accepted for retry, got %d body=%q", retryResp.StatusCode, string(payload))
	}

	var out struct {
		Accepted bool       `json:"accepted"`
		Reason   string     `json:"reason"`
		Op       *Operation `json:"op"`
	}
	if decodeErr := json.NewDecoder(retryResp.Body).Decode(&out); decodeErr != nil {
		t.Fatalf("decode webhook retry response: %v", decodeErr)
	}
	if !out.Accepted {
		t.Fatalf("expected accepted=true on retry, got reason=%q", out.Reason)
	}
	if out.Op == nil || strings.TrimSpace(out.Op.ID) == "" {
		t.Fatalf("expected op.id in retry response, got %#v", out.Op)
	}
}

func TestAPI_DeploymentAllowsRetryAfterActiveOperationTerminal(t *testing.T) {
	fixture := newAsyncAPIFixture(t, opEventsHeartbeatInterval)
	defer fixture.Close()

	projectID := "project-ops-conflict-deploy"
	activeOpID := "op-running-deploy"
	spec := testProjectSpec("deploy-conflict")
	putProjectFixture(t, fixture, projectID, spec, activeOpID, OpCI)
	putOpFixture(t, fixture, activeOpID, projectID, OpCI, opStatusRunning)

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	body, err := json.Marshal(map[string]any{
		"project_id":  projectID,
		"environment": "dev",
	})
	if err != nil {
		t.Fatalf("marshal deployment payload: %v", err)
	}

	resp, err := http.Post(srv.URL+"/api/events/deployment", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request deployment conflict: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 409 conflict, got %d body=%q", resp.StatusCode, string(payload))
	}

	putOpFixture(t, fixture, activeOpID, projectID, OpCI, opStatusDone)

	retryResp, err := http.Post(srv.URL+"/api/events/deployment", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("request deployment retry: %v", err)
	}
	defer retryResp.Body.Close()
	if retryResp.StatusCode != http.StatusAccepted {
		payload, _ := io.ReadAll(retryResp.Body)
		t.Fatalf("expected 202 accepted, got %d body=%q", retryResp.StatusCode, string(payload))
	}

	var out struct {
		Accepted bool       `json:"accepted"`
		Op       *Operation `json:"op"`
	}
	if decodeErr := json.NewDecoder(retryResp.Body).Decode(&out); decodeErr != nil {
		t.Fatalf("decode deployment retry response: %v", decodeErr)
	}
	if !out.Accepted {
		t.Fatal("expected accepted=true for deployment retry")
	}
	if out.Op == nil || strings.TrimSpace(out.Op.ID) == "" {
		t.Fatalf("expected op.id in deployment retry response, got %#v", out.Op)
	}
}

func TestAPI_DeleteOpEventsIncludeTerminalWhenProjectRecordMissing(t *testing.T) {
	fixture := newAsyncAPIFixture(t, 40*time.Millisecond)
	defer fixture.Close()

	projectID := "project-delete-op-events-1"
	opID := "op-delete-op-events-1"
	spec := testProjectSpec("delete-op-events")
	putProjectFixture(t, fixture, projectID, spec, opID, OpDelete)
	putOpFixture(t, fixture, opID, projectID, OpDelete, opStatusRunning)

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/ops/"+opID+"/events", nil)
	if err != nil {
		t.Fatalf("build delete-op events request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open delete-op sse stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%q", resp.StatusCode, string(payload))
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
	if decodeErr := json.Unmarshal([]byte(bootstrap.data), &bootstrapPayload); decodeErr != nil {
		t.Fatalf("decode bootstrap payload: %v", decodeErr)
	}
	if bootstrapPayload.OpID != opID {
		t.Fatalf("expected bootstrap op id %q, got %q", opID, bootstrapPayload.OpID)
	}
	if bootstrapPayload.Status != opStatusRunning {
		t.Fatalf("expected bootstrap status %q, got %q", opStatusRunning, bootstrapPayload.Status)
	}

	if err := fixture.api.store.DeleteProject(context.Background(), projectID); err != nil {
		t.Fatalf("delete project before finalize: %v", err)
	}
	if err := finalizeOp(
		context.Background(),
		fixture.api.store,
		opID,
		projectID,
		OpDelete,
		opStatusDone,
		"",
	); err != nil {
		t.Fatalf("finalize delete op with missing project: %v", err)
	}

	status := waitForSSEEvent(t, events, errCh, opEventStatus, 2*time.Second)
	var statusPayload opEventPayload
	if decodeErr := json.Unmarshal([]byte(status.data), &statusPayload); decodeErr != nil {
		t.Fatalf("decode status payload: %v", decodeErr)
	}
	if statusPayload.Status != opStatusDone {
		t.Fatalf("expected status event status %q, got %q", opStatusDone, statusPayload.Status)
	}

	completed := waitForSSEEvent(t, events, errCh, opEventCompleted, 2*time.Second)
	var completedPayload opEventPayload
	if decodeErr := json.Unmarshal([]byte(completed.data), &completedPayload); decodeErr != nil {
		t.Fatalf("decode completed payload: %v", decodeErr)
	}
	if completedPayload.OpID != opID {
		t.Fatalf("expected completed op id %q, got %q", opID, completedPayload.OpID)
	}
	if completedPayload.Kind != OpDelete {
		t.Fatalf("expected completed kind %q, got %q", OpDelete, completedPayload.Kind)
	}
	if completedPayload.Status != opStatusDone {
		t.Fatalf("expected completed status %q, got %q", opStatusDone, completedPayload.Status)
	}
}
