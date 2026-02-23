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

func TestNewOpBootstrapSnapshotReconstructsLatestStepFromStoredOp(t *testing.T) {
	t.Parallel()

	requestedAt := time.Date(2026, time.January, 2, 3, 4, 0, 0, time.UTC)
	firstStarted := requestedAt.Add(1 * time.Minute)
	firstEnded := firstStarted.Add(10 * time.Second)
	secondStarted := firstEnded.Add(15 * time.Second)

	op := Operation{
		ID:        "op-bootstrap-running",
		Kind:      OpCreate,
		ProjectID: "project-running",
		Delivery: DeliveryLifecycle{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
		Requested: requestedAt,
		Finished:  time.Time{},
		Status:    opStatusRunning,
		Error:     "",
		Steps: []OpStep{
			{
				Worker:    "registrar",
				StartedAt: firstStarted,
				EndedAt:   firstEnded,
				Message:   "registered project config",
				Error:     "",
				Artifacts: []string{"registration/project.yaml"},
			},
			{
				Worker:    "repoBootstrap",
				StartedAt: secondStarted,
				EndedAt:   time.Time{},
				Message:   "bootstrapping local repos",
				Error:     "",
				Artifacts: nil,
			},
		},
	}

	payload := newOpBootstrapSnapshot(op)
	if payload.OpID != op.ID {
		t.Fatalf("expected op id %q, got %q", op.ID, payload.OpID)
	}
	if payload.Status != opStatusRunning {
		t.Fatalf("expected status %q, got %q", opStatusRunning, payload.Status)
	}
	if payload.Worker != "repoBootstrap" {
		t.Fatalf("expected latest worker repoBootstrap, got %q", payload.Worker)
	}
	if payload.StepIndex != 2 {
		t.Fatalf("expected latest step index 2, got %d", payload.StepIndex)
	}
	if payload.Message != "bootstrapping local repos" {
		t.Fatalf("unexpected latest step message: %q", payload.Message)
	}
	if payload.TotalSteps != opTotalStepsFullChain {
		t.Fatalf("expected total steps %d, got %d", opTotalStepsFullChain, payload.TotalSteps)
	}
	if payload.ProgressPercent != 25 {
		t.Fatalf("expected progress 25, got %d", payload.ProgressPercent)
	}
	if !payload.At.Equal(secondStarted) {
		t.Fatalf("expected snapshot timestamp %s, got %s", secondStarted, payload.At)
	}
}

func TestNewOpBootstrapSnapshotUsesTerminalStateFromStoredOp(t *testing.T) {
	t.Parallel()

	requestedAt := time.Date(2026, time.January, 4, 10, 0, 0, 0, time.UTC)
	stepStarted := requestedAt.Add(20 * time.Second)
	stepEnded := stepStarted.Add(3 * time.Second)
	finishedAt := stepEnded.Add(1 * time.Second)

	op := Operation{
		ID:        "op-bootstrap-error",
		Kind:      OpDeploy,
		ProjectID: "project-error",
		Delivery: DeliveryLifecycle{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
		Requested: requestedAt,
		Finished:  finishedAt,
		Status:    opStatusError,
		Error:     "no build image found for deployment",
		Steps: []OpStep{
			{
				Worker:    "manifestRenderer",
				StartedAt: stepStarted,
				EndedAt:   stepEnded,
				Message:   "",
				Error:     "",
				Artifacts: []string{
					"deploy/dev/rendered.yaml",
					"deploy/dev/deployment.yaml",
				},
			},
		},
	}

	payload := newOpBootstrapSnapshot(op)
	if payload.Status != opStatusError {
		t.Fatalf("expected status %q, got %q", opStatusError, payload.Status)
	}
	if payload.Message != "operation failed" {
		t.Fatalf("expected failure message, got %q", payload.Message)
	}
	if payload.Error != op.Error {
		t.Fatalf("expected op error %q, got %q", op.Error, payload.Error)
	}
	if payload.Hint == "" {
		t.Fatal("expected non-empty hint for terminal error")
	}
	if !payload.At.Equal(finishedAt) {
		t.Fatalf("expected finished timestamp %s, got %s", finishedAt, payload.At)
	}
	if payload.DurationMS != stepEnded.Sub(stepStarted).Milliseconds() {
		t.Fatalf(
			"expected duration %dms, got %dms",
			stepEnded.Sub(stepStarted).Milliseconds(),
			payload.DurationMS,
		)
	}
	if len(payload.Artifacts) != 2 {
		t.Fatalf("expected bootstrap artifacts from latest step, got %d", len(payload.Artifacts))
	}
}

func TestOpTotalStepsForPromotionAndRelease(t *testing.T) {
	t.Parallel()

	if got := opTotalSteps(OpPromote); got != opTotalStepsTransition {
		t.Fatalf("expected promote total steps %d, got %d", opTotalStepsTransition, got)
	}
	if got := opTotalSteps(OpRelease); got != opTotalStepsTransition {
		t.Fatalf("expected release total steps %d, got %d", opTotalStepsTransition, got)
	}
}

func TestOpProgressPercentForPromotionStages(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	op := Operation{
		ID:        "op-progress-promote",
		Kind:      OpPromote,
		ProjectID: "project-progress-promote",
		Delivery: DeliveryLifecycle{
			Stage:       DeliveryStagePromote,
			Environment: "",
			FromEnv:     "dev",
			ToEnv:       "staging",
		},
		Requested: now.Add(-2 * time.Minute),
		Finished:  time.Time{},
		Status:    opStatusRunning,
		Error:     "",
		Steps: []OpStep{
			{
				Worker:    promotionStepPlan,
				StartedAt: now.Add(-90 * time.Second),
				EndedAt:   now.Add(-89 * time.Second),
				Message:   "planned transition",
				Error:     "",
				Artifacts: nil,
			},
			{
				Worker:    promotionStepRender,
				StartedAt: now.Add(-75 * time.Second),
				EndedAt:   now.Add(-70 * time.Second),
				Message:   "rendered transition manifests",
				Error:     "",
				Artifacts: []string{"promotions/dev-to-staging/rendered.yaml"},
			},
			{
				Worker:    promotionStepCommit,
				StartedAt: now.Add(-60 * time.Second),
				EndedAt:   time.Time{},
				Message:   "committing transition manifests",
				Error:     "",
				Artifacts: nil,
			},
		},
	}
	if got := opProgressPercent(op); got != 50 {
		t.Fatalf("expected progress 50 for 2/4 finished promotion steps, got %d", got)
	}
}
