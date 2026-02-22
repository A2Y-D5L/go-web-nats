//nolint:testpackage // Event-hub tests validate unexported replay and retention behavior.
package platform

import (
	"testing"
	"time"
)

func newTestOpEventPayload(opID, projectID string, kind OperationKind, status string) opEventPayload {
	return opEventPayload{
		EventID:         "",
		Sequence:        0,
		OpID:            opID,
		ProjectID:       projectID,
		Kind:            kind,
		Status:          status,
		At:              time.Now().UTC(),
		Worker:          "",
		StepIndex:       0,
		TotalSteps:      0,
		ProgressPercent: 0,
		DurationMS:      0,
		Message:         "",
		Error:           "",
		Artifacts:       nil,
		Delivery: opEventDelivery{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
		Hint: "",
	}
}

func TestOpEventHubReplayAndTrim(t *testing.T) {
	hub := newOpEventHub(3, time.Minute)
	base := newTestOpEventPayload("op-1", "project-1", OpCreate, opStatusRunning)

	for range 4 {
		hub.publish(opEventStatus, base)
	}

	replay, live, needsBootstrap, unsubscribe := hub.subscribe("op-1", "2")
	defer unsubscribe()
	if live == nil {
		t.Fatal("expected live channel")
	}
	if needsBootstrap {
		t.Fatal("expected replay without bootstrap for in-range Last-Event-ID")
	}
	if len(replay) != 2 {
		t.Fatalf("expected 2 replay events, got %d", len(replay))
	}
	if replay[0].Payload.Sequence != 3 || replay[1].Payload.Sequence != 4 {
		t.Fatalf(
			"unexpected replay sequence order: got [%d, %d]",
			replay[0].Payload.Sequence,
			replay[1].Payload.Sequence,
		)
	}
}

func TestOpEventHubOutOfRangeRequiresBootstrap(t *testing.T) {
	hub := newOpEventHub(2, time.Minute)
	base := newTestOpEventPayload("op-2", "project-2", OpDeploy, opStatusRunning)

	for range 3 {
		hub.publish(opEventStatus, base)
	}

	replay, _, needsBootstrap, unsubscribe := hub.subscribe("op-2", "0")
	defer unsubscribe()
	if !needsBootstrap {
		t.Fatal("expected bootstrap when Last-Event-ID is outside retained replay window")
	}
	if len(replay) != 0 {
		t.Fatalf("expected no replay for out-of-range Last-Event-ID, got %d", len(replay))
	}
}

func TestOpEventHubTerminalTTLPrune(t *testing.T) {
	hub := newOpEventHub(8, 25*time.Millisecond)
	hub.publish(
		opEventCompleted,
		newTestOpEventPayload("terminal-op", "project-3", OpRelease, opStatusDone),
	)

	time.Sleep(50 * time.Millisecond)
	hub.publish(
		opEventStatus,
		newTestOpEventPayload("other-op", "project-4", OpCreate, statusMessageQueued),
	)

	if got := hub.latestSequence("terminal-op"); got != 0 {
		t.Fatalf("expected terminal stream to be pruned after ttl, latest sequence=%d", got)
	}
}
