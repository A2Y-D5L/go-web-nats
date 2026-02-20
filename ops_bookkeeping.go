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
	op.Status = "running"
	op.Steps = append(op.Steps, OpStep{
		Worker:    worker,
		StartedAt: startedAt,
		EndedAt:   time.Time{},
		Message:   msg,
		Error:     "",
		Artifacts: nil,
	})
	return store.PutOp(ctx, op)
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
	// Find last step for worker that doesn't have EndedAt set.
	for i := len(op.Steps) - 1; i >= 0; i-- {
		if op.Steps[i].Worker == worker && op.Steps[i].EndedAt.IsZero() {
			op.Steps[i].EndedAt = endedAt
			if message != "" {
				op.Steps[i].Message = message
			}
			op.Steps[i].Error = stepErr
			op.Steps[i].Artifacts = artifacts
			break
		}
	}
	if stepErr != "" {
		op.Status = "error"
		op.Error = stepErr
		op.Finished = time.Now().UTC()
	}
	return store.PutOp(ctx, op)
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
	return nil
}
