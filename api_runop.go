package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (a *API) runOp(
	ctx context.Context,
	kind OperationKind,
	projectID string,
	spec ProjectSpec,
) (Operation, WorkerResultMsg, error) {
	apiLog := appLoggerForProcess().Source("api")
	opID := newID()
	now := time.Now().UTC()

	// Persist initial op record
	op := Operation{
		ID:        opID,
		Kind:      kind,
		ProjectID: projectID,
		Requested: now,
		Finished:  time.Time{},
		Status:    "queued",
		Error:     "",
		Steps:     []OpStep{},
	}
	if err := a.store.PutOp(ctx, op); err != nil {
		return Operation{}, WorkerResultMsg{}, fmt.Errorf("persist op: %w", err)
	}
	apiLog.Infof("queued op=%s kind=%s project=%s", opID, kind, projectID)

	// Update project status (except delete might be removed later)
	if kind != OpDelete {
		p, err := a.store.GetProject(ctx, projectID)
		if err == nil {
			p.Spec = spec
			msg := "queued"
			if kind == OpCI {
				msg = "queued ci from source webhook"
			}
			p.Status = ProjectStatus{
				Phase:      "Reconciling",
				UpdatedAt:  now,
				LastOpID:   opID,
				LastOpKind: string(kind),
				Message:    msg,
			}
			_ = a.store.PutProject(ctx, p)
		}
	} else {
		_ = finalizeOp(ctx, a.store, opID, projectID, kind, "running", "")
	}

	// Register waiter before publish
	ch := a.waiters.register(opID)
	defer a.waiters.unregister(opID)

	// Publish start message
	msg := ProjectOpMsg{
		OpID:      opID,
		Kind:      kind,
		ProjectID: projectID,
		Spec:      spec,
		Err:       "",
		At:        now,
	}
	b, _ := json.Marshal(msg)
	startSubject := subjectProjectOpStart
	if kind == OpCI {
		startSubject = subjectBootstrapDone
	}
	finalizeCtx := context.WithoutCancel(ctx)
	if err := a.nc.Publish(startSubject, b); err != nil {
		_ = finalizeOp(finalizeCtx, a.store, opID, projectID, kind, "error", err.Error())
		apiLog.Errorf("publish failed op=%s kind=%s project=%s: %v", opID, kind, projectID, err)
		return Operation{}, WorkerResultMsg{}, fmt.Errorf("publish op: %w", err)
	}
	apiLog.Debugf("published op=%s subject=%s", opID, startSubject)

	// Wait for final worker completion
	waitCtx, cancel := context.WithTimeout(ctx, apiWaitTimeout)
	defer cancel()

	var final WorkerResultMsg
	select {
	case <-waitCtx.Done():
		_ = finalizeOp(
			finalizeCtx,
			a.store,
			opID,
			projectID,
			kind,
			"error",
			"timeout waiting for workers",
		)
		apiLog.Errorf("timeout op=%s kind=%s project=%s", opID, kind, projectID)
		return Operation{}, WorkerResultMsg{}, errors.New("timeout waiting for workers")
	case final = <-ch:
	}

	if final.Err != "" {
		_ = finalizeOp(finalizeCtx, a.store, opID, projectID, kind, "error", final.Err)
		apiLog.Errorf("op=%s failed in %s: %s", opID, final.Worker, final.Err)
		return Operation{}, final, errors.New(final.Err)
	}

	// Fetch final op state for response
	op, _ = a.store.GetOp(ctx, opID)
	apiLog.Infof("completed op=%s kind=%s project=%s", opID, kind, projectID)
	return op, final, nil
}
