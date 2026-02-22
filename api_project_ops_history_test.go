//nolint:testpackage,exhaustruct // API history tests need internal runtime wiring and concise fixtures.
package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

type projectOpsHistoryFixture struct {
	api   *API
	close func()
}

type projectOpsListItemForTest struct {
	ID                string        `json:"id"`
	Kind              OperationKind `json:"kind"`
	Status            string        `json:"status"`
	Requested         time.Time     `json:"requested"`
	Finished          time.Time     `json:"finished"`
	Error             string        `json:"error"`
	SummaryMessage    string        `json:"summary_message"`
	LastEventSequence int64         `json:"last_event_sequence"`
	LastUpdateAt      time.Time     `json:"last_update_at"`
}

type projectOpsListResponseForTest struct {
	Items      []projectOpsListItemForTest `json:"items"`
	NextCursor string                      `json:"next_cursor"`
}

func newProjectOpsHistoryFixture(t *testing.T) *projectOpsHistoryFixture {
	t.Helper()

	workerFixture := newWorkerDeliveryFixture(t)
	hub := newOpEventHub(opEventsHistoryLimit, opEventsRetention)
	workerFixture.store.setOpEvents(hub)

	api := &API{
		nc:                  workerFixture.nc,
		store:               workerFixture.store,
		artifacts:           NewFSArtifacts(t.TempDir()),
		waiters:             newWaiterHub(),
		opEvents:            hub,
		opHeartbeatInterval: opEventsHeartbeatInterval,
		sourceTriggerMu:     sync.Mutex{},
		projectStartLocksMu: sync.Mutex{},
		projectStartLocks:   map[string]*sync.Mutex{},
	}
	return &projectOpsHistoryFixture{
		api: api,
		close: func() {
			workerFixture.close()
		},
	}
}

func (f *projectOpsHistoryFixture) Close() {
	if f == nil || f.close == nil {
		return
	}
	f.close()
}

func projectSpecForOpsHistoryTest(name string) ProjectSpec {
	return normalizeProjectSpec(ProjectSpec{
		APIVersion: projectAPIVersion,
		Kind:       projectKind,
		Name:       name,
		Runtime:    "go_1.26",
		Capabilities: []string{
			"http",
		},
		Environments: map[string]EnvConfig{
			"dev": {
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
}

func putProjectOpsHistoryFixture(
	t *testing.T,
	store *Store,
	projectID string,
) {
	t.Helper()
	now := time.Now().UTC()
	project := Project{
		ID:        projectID,
		CreatedAt: now,
		UpdatedAt: now,
		Spec:      projectSpecForOpsHistoryTest("history-" + projectID),
		Status: ProjectStatus{
			Phase:      projectPhaseReady,
			UpdatedAt:  now,
			LastOpID:   "",
			LastOpKind: "",
			Message:    "ready",
		},
	}
	if err := store.PutProject(context.Background(), project); err != nil {
		t.Fatalf("put project fixture: %v", err)
	}
}

func putOpHistoryFixture(
	t *testing.T,
	store *Store,
	op Operation,
) {
	t.Helper()
	if err := store.PutOp(context.Background(), op); err != nil {
		t.Fatalf("put op fixture: %v", err)
	}
}

func fetchProjectOpsHistory(
	t *testing.T,
	client *http.Client,
	url string,
) projectOpsListResponseForTest {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("request project ops history: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body projectOpsListResponseForTest
	if decodeErr := json.NewDecoder(resp.Body).Decode(&body); decodeErr != nil {
		t.Fatalf("decode project ops history response: %v", decodeErr)
	}
	return body
}

func TestAPI_ProjectOpsHistoryOrderingLimitAndIsolation(t *testing.T) {
	fixture := newProjectOpsHistoryFixture(t)
	defer fixture.Close()

	const (
		projectA = "project-history-a"
		projectB = "project-history-b"
	)
	putProjectOpsHistoryFixture(t, fixture.api.store, projectA)
	putProjectOpsHistoryFixture(t, fixture.api.store, projectB)

	base := time.Now().UTC().Add(-15 * time.Minute)
	opA1 := Operation{
		ID:        "op-history-a1",
		Kind:      OpCreate,
		ProjectID: projectA,
		Requested: base.Add(1 * time.Minute),
		Finished:  base.Add(2 * time.Minute),
		Status:    opStatusDone,
		Error:     "",
		Steps: []OpStep{
			{
				Worker:    "registrar",
				StartedAt: base.Add(1 * time.Minute),
				EndedAt:   base.Add(2 * time.Minute),
				Message:   "registered app metadata",
				Error:     "",
				Artifacts: nil,
			},
		},
	}
	opA2 := Operation{
		ID:        "op-history-a2",
		Kind:      OpCI,
		ProjectID: projectA,
		Requested: base.Add(3 * time.Minute),
		Finished:  base.Add(4 * time.Minute),
		Status:    opStatusDone,
		Error:     "",
		Steps: []OpStep{
			{
				Worker:    "imageBuilder",
				StartedAt: base.Add(3 * time.Minute),
				EndedAt:   base.Add(4 * time.Minute),
				Message:   "image build completed",
				Error:     "",
				Artifacts: []string{"build/image.txt"},
			},
		},
	}
	opA3 := Operation{
		ID:        "op-history-a3",
		Kind:      OpDeploy,
		ProjectID: projectA,
		Requested: base.Add(5 * time.Minute),
		Finished:  time.Time{},
		Status:    opStatusRunning,
		Error:     "",
		Steps: []OpStep{
			{
				Worker:    "deployer",
				StartedAt: base.Add(5 * time.Minute),
				EndedAt:   time.Time{},
				Message:   "rendering deployment assets",
				Error:     "",
				Artifacts: nil,
			},
		},
	}
	opB1 := Operation{
		ID:        "op-history-b1",
		Kind:      OpUpdate,
		ProjectID: projectB,
		Requested: base.Add(6 * time.Minute),
		Finished:  base.Add(7 * time.Minute),
		Status:    opStatusDone,
		Error:     "",
		Steps: []OpStep{
			{
				Worker:    "registrar",
				StartedAt: base.Add(6 * time.Minute),
				EndedAt:   base.Add(7 * time.Minute),
				Message:   "updated app configuration",
				Error:     "",
				Artifacts: nil,
			},
		},
	}

	putOpHistoryFixture(t, fixture.api.store, opA1)
	putOpHistoryFixture(t, fixture.api.store, opA2)
	putOpHistoryFixture(t, fixture.api.store, opA3)
	putOpHistoryFixture(t, fixture.api.store, opB1)

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	pageOne := fetchProjectOpsHistory(
		t,
		srv.Client(),
		fmt.Sprintf("%s/api/projects/%s/ops?limit=2", srv.URL, projectA),
	)

	if len(pageOne.Items) != 2 {
		t.Fatalf("expected 2 items on first page, got %d", len(pageOne.Items))
	}
	if pageOne.Items[0].ID != opA3.ID || pageOne.Items[1].ID != opA2.ID {
		t.Fatalf("unexpected first page order: %#v", pageOne.Items)
	}
	if pageOne.NextCursor != opA2.ID {
		t.Fatalf("expected next_cursor %q, got %q", opA2.ID, pageOne.NextCursor)
	}
	if pageOne.Items[0].SummaryMessage != "rendering deployment assets" {
		t.Fatalf("expected summary message from latest step, got %q", pageOne.Items[0].SummaryMessage)
	}
	if pageOne.Items[0].LastEventSequence < 0 {
		t.Fatalf("expected non-negative last_event_sequence, got %d", pageOne.Items[0].LastEventSequence)
	}
	if pageOne.Items[0].LastUpdateAt.IsZero() {
		t.Fatal("expected last_update_at in list response")
	}
	for _, item := range pageOne.Items {
		if item.ID == opB1.ID {
			t.Fatalf("unexpected cross-project operation in history: %q", item.ID)
		}
	}

	pageTwo := fetchProjectOpsHistory(
		t,
		srv.Client(),
		fmt.Sprintf(
			"%s/api/projects/%s/ops?limit=2&cursor=%s",
			srv.URL,
			projectA,
			url.QueryEscape(pageOne.NextCursor),
		),
	)
	if len(pageTwo.Items) != 1 {
		t.Fatalf("expected 1 item on second page, got %d", len(pageTwo.Items))
	}
	if pageTwo.Items[0].ID != opA1.ID {
		t.Fatalf("expected second page item %q, got %q", opA1.ID, pageTwo.Items[0].ID)
	}
	if pageTwo.NextCursor != "" {
		t.Fatalf("expected empty next_cursor on terminal page, got %q", pageTwo.NextCursor)
	}
}

func TestAPI_ProjectOpsHistorySupportsBeforeAndRejectsInvalidLimit(t *testing.T) {
	fixture := newProjectOpsHistoryFixture(t)
	defer fixture.Close()

	const projectID = "project-history-before"
	putProjectOpsHistoryFixture(t, fixture.api.store, projectID)

	base := time.Now().UTC().Add(-10 * time.Minute)
	opOne := Operation{
		ID:        "op-history-before-1",
		Kind:      OpCreate,
		ProjectID: projectID,
		Requested: base.Add(1 * time.Minute),
		Finished:  base.Add(2 * time.Minute),
		Status:    opStatusDone,
		Error:     "",
		Steps:     []OpStep{},
	}
	opTwo := Operation{
		ID:        "op-history-before-2",
		Kind:      OpUpdate,
		ProjectID: projectID,
		Requested: base.Add(3 * time.Minute),
		Finished:  base.Add(4 * time.Minute),
		Status:    opStatusDone,
		Error:     "",
		Steps:     []OpStep{},
	}

	putOpHistoryFixture(t, fixture.api.store, opOne)
	putOpHistoryFixture(t, fixture.api.store, opTwo)

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	beforeByOpID := fetchProjectOpsHistory(
		t,
		srv.Client(),
		fmt.Sprintf(
			"%s/api/projects/%s/ops?before=%s",
			srv.URL,
			projectID,
			url.QueryEscape(opTwo.ID),
		),
	)
	if len(beforeByOpID.Items) != 1 || beforeByOpID.Items[0].ID != opOne.ID {
		t.Fatalf("unexpected before=op-id result: %#v", beforeByOpID.Items)
	}

	beforeByTimestamp := fetchProjectOpsHistory(
		t,
		srv.Client(),
		fmt.Sprintf(
			"%s/api/projects/%s/ops?before=%s",
			srv.URL,
			projectID,
			url.QueryEscape(opTwo.Requested.Format(time.RFC3339Nano)),
		),
	)
	if len(beforeByTimestamp.Items) != 1 || beforeByTimestamp.Items[0].ID != opOne.ID {
		t.Fatalf("unexpected before=timestamp result: %#v", beforeByTimestamp.Items)
	}

	resp, err := srv.Client().Get(fmt.Sprintf("%s/api/projects/%s/ops?limit=bad", srv.URL, projectID))
	if err != nil {
		t.Fatalf("request invalid limit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid limit, got %d", resp.StatusCode)
	}
}

func TestAPI_ProjectOpsHistoryRealtimeTerminalCoherence(t *testing.T) {
	fixture := newProjectOpsHistoryFixture(t)
	defer fixture.Close()

	const projectID = "project-history-realtime"
	putProjectOpsHistoryFixture(t, fixture.api.store, projectID)

	base := time.Now().UTC().Add(-5 * time.Minute)
	running := Operation{
		ID:        "op-history-realtime-1",
		Kind:      OpDeploy,
		ProjectID: projectID,
		Requested: base,
		Finished:  time.Time{},
		Status:    opStatusRunning,
		Error:     "",
		Steps: []OpStep{
			{
				Worker:    "deployer",
				StartedAt: base.Add(5 * time.Second),
				EndedAt:   time.Time{},
				Message:   "rendering deployment assets",
				Error:     "",
				Artifacts: nil,
			},
		},
	}
	putOpHistoryFixture(t, fixture.api.store, running)
	emitOpBootstrap(fixture.api.opEvents, running, "operation accepted and queued")
	emitOpStatus(fixture.api.opEvents, running, "operation status updated")

	srv := httptest.NewServer(fixture.api.routes())
	defer srv.Close()

	first := fetchProjectOpsHistory(
		t,
		srv.Client(),
		fmt.Sprintf("%s/api/projects/%s/ops?limit=5", srv.URL, projectID),
	)
	if len(first.Items) != 1 {
		t.Fatalf("expected one history row, got %d", len(first.Items))
	}
	if first.Items[0].Status != opStatusRunning {
		t.Fatalf("expected running status in history, got %q", first.Items[0].Status)
	}
	if first.Items[0].LastEventSequence < 2 {
		t.Fatalf("expected sequence >=2 after realtime events, got %d", first.Items[0].LastEventSequence)
	}

	done := running
	done.Status = opStatusDone
	done.Finished = base.Add(30 * time.Second)
	done.Steps[0].EndedAt = base.Add(28 * time.Second)
	done.Steps[0].Message = "deployment rendered and published"
	putOpHistoryFixture(t, fixture.api.store, done)
	emitOpStatus(fixture.api.opEvents, done, "operation status updated")
	emitOpTerminal(fixture.api.opEvents, done)

	second := fetchProjectOpsHistory(
		t,
		srv.Client(),
		fmt.Sprintf("%s/api/projects/%s/ops?limit=5", srv.URL, projectID),
	)
	if len(second.Items) != 1 {
		t.Fatalf("expected one history row after terminal update, got %d", len(second.Items))
	}
	item := second.Items[0]
	if item.Status != opStatusDone {
		t.Fatalf("expected done status in history, got %q", item.Status)
	}
	if item.LastEventSequence <= first.Items[0].LastEventSequence {
		t.Fatalf(
			"expected terminal sequence to advance (before=%d after=%d)",
			first.Items[0].LastEventSequence,
			item.LastEventSequence,
		)
	}
	if item.SummaryMessage != "deployment rendered and published" {
		t.Fatalf("expected final summary message from persisted op, got %q", item.SummaryMessage)
	}
}
