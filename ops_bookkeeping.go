package platform

import (
	"context"
	"errors"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

////////////////////////////////////////////////////////////////////////////////
// Operation bookkeeping helpers
////////////////////////////////////////////////////////////////////////////////

func markOpStepStart(
	ctx context.Context,
	store *Store,
	opID, worker string,
	startedAt time.Time,
	msg string,
) error {
	op, err := store.GetOp(ctx, opID)
	if err != nil {
		return err
	}
	prevStatus := op.Status
	op.Status = opStatusRunning
	op.Steps = append(op.Steps, OpStep{
		Worker:    worker,
		StartedAt: startedAt,
		EndedAt:   time.Time{},
		Message:   msg,
		Error:     "",
		Artifacts: nil,
	})
	putErr := store.PutOp(ctx, op)
	if putErr != nil {
		return putErr
	}

	if prevStatus != op.Status {
		emitOpStatus(store.opEvents, op, "operation started")
	}
	emitOpStepStarted(store.opEvents, op, worker, len(op.Steps), msg)
	return nil
}

func markOpStepEnd(
	ctx context.Context,
	store *Store,
	opID, worker string,
	endedAt time.Time,
	message, stepErr string,
	artifacts []string,
) error {
	op, err := store.GetOp(ctx, opID)
	if err != nil {
		return err
	}
	prevStatus := op.Status
	stepIndex := 0
	var stepStartedAt time.Time
	// Find last step for worker that doesn't have EndedAt set.
	for i := len(op.Steps) - 1; i >= 0; i-- {
		if op.Steps[i].Worker == worker && op.Steps[i].EndedAt.IsZero() {
			op.Steps[i].EndedAt = endedAt
			if message != "" {
				op.Steps[i].Message = message
			}
			op.Steps[i].Error = stepErr
			op.Steps[i].Artifacts = artifacts
			stepIndex = i + 1
			stepStartedAt = op.Steps[i].StartedAt
			break
		}
	}
	if stepErr != "" {
		op.Status = opStatusError
		op.Error = stepErr
		op.Finished = time.Now().UTC()
	}
	putErr := store.PutOp(ctx, op)
	if putErr != nil {
		return putErr
	}

	if prevStatus != op.Status {
		emitOpStatus(store.opEvents, op, "operation status updated")
	}
	if stepIndex > 0 {
		emitOpStepEnded(
			store.opEvents,
			op,
			worker,
			stepIndex,
			message,
			stepErr,
			artifacts,
			stepStartedAt,
			endedAt,
		)
	}
	if stepErr != "" {
		emitOpTerminal(store.opEvents, op)
	}
	return nil
}

func finalizeOp(
	ctx context.Context,
	store *Store,
	opID, projectID string,
	kind OperationKind,
	status, errMsg string,
) error {
	op, err := store.GetOp(ctx, opID)
	if err != nil {
		return err
	}
	prevStatus := op.Status
	prevError := op.Error
	op.Status = status
	op.Error = errMsg
	op.Finished = time.Now().UTC()
	putErr := store.PutOp(ctx, op)
	if putErr != nil {
		return putErr
	}

	// Best-effort: update project status (except delete where record might be removed later)
	p, err := store.GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil
		}
		return err
	}
	switch {
	case kind == OpDelete && status == "running":
		p.Status.Phase = projectPhaseDel
	case status == "error":
		p.Status.Phase = projectPhaseError
		p.Status.Message = errMsg
	case status == "done":
		if kind != OpDelete {
			p.Status.Phase = projectPhaseReady
			p.Status.Message = "ready"
		}
	}
	p.Status.UpdatedAt = time.Now().UTC()
	p.Status.LastOpID = opID
	p.Status.LastOpKind = string(kind)
	_ = store.PutProject(ctx, p)

	if prevStatus != op.Status || prevError != op.Error {
		emitOpStatus(store.opEvents, op, "operation status updated")
	}
	if status == "done" || status == "error" {
		emitOpTerminal(store.opEvents, op)
	}
	return nil
}
