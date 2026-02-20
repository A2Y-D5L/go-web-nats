package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type opRunOptions struct {
	deployEnv string
	fromEnv   string
	toEnv     string
}

func emptyOpRunOptions() opRunOptions {
	return opRunOptions{deployEnv: "", fromEnv: "", toEnv: ""}
}

func deployOpRunOptions(env string) opRunOptions {
	return opRunOptions{deployEnv: env, fromEnv: "", toEnv: ""}
}

func promotionOpRunOptions(fromEnv, toEnv string) opRunOptions {
	return opRunOptions{deployEnv: "", fromEnv: fromEnv, toEnv: toEnv}
}

func (a *API) runOp(
	ctx context.Context,
	kind OperationKind,
	projectID string,
	spec ProjectSpec,
	opts opRunOptions,
) (Operation, WorkerResultMsg, error) {
	apiLog := appLoggerForProcess().Source("api")
	opID := newID()
	now := time.Now().UTC()

	op := Operation{
		ID:        opID,
		Kind:      kind,
		ProjectID: projectID,
		Requested: now,
		Finished:  time.Time{},
		Status:    statusMessageQueued,
		Error:     "",
		Steps:     []OpStep{},
	}
	if err := a.store.PutOp(ctx, op); err != nil {
		return Operation{}, WorkerResultMsg{}, fmt.Errorf("persist op: %w", err)
	}
	apiLog.Infof("queued op=%s kind=%s project=%s", opID, kind, projectID)

	if kind != OpDelete {
		a.setQueuedProjectStatus(ctx, opID, kind, projectID, spec, now)
	} else {
		_ = finalizeOp(ctx, a.store, opID, projectID, kind, "running", "")
	}

	ch := a.waiters.register(opID)
	defer a.waiters.unregister(opID)

	opMsg := newProjectOpMsg(opID, kind, projectID, spec, opts, now)
	body, _ := json.Marshal(opMsg)
	startSubject := startSubjectForOperation(kind)

	finalizeCtx := context.WithoutCancel(ctx)
	if err := a.nc.Publish(startSubject, body); err != nil {
		_ = finalizeOp(finalizeCtx, a.store, opID, projectID, kind, "error", err.Error())
		apiLog.Errorf("publish failed op=%s kind=%s project=%s: %v", opID, kind, projectID, err)
		return Operation{}, WorkerResultMsg{}, fmt.Errorf("publish op: %w", err)
	}
	apiLog.Debugf("published op=%s subject=%s", opID, startSubject)

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

	op, _ = a.store.GetOp(ctx, opID)
	apiLog.Infof("completed op=%s kind=%s project=%s", opID, kind, projectID)
	return op, final, nil
}

func (a *API) setQueuedProjectStatus(
	ctx context.Context,
	opID string,
	kind OperationKind,
	projectID string,
	spec ProjectSpec,
	now time.Time,
) {
	project, err := a.store.GetProject(ctx, projectID)
	if err != nil {
		return
	}
	project.Spec = spec
	project.Status = ProjectStatus{
		Phase:      "Reconciling",
		UpdatedAt:  now,
		LastOpID:   opID,
		LastOpKind: string(kind),
		Message:    queuedProjectMessage(kind),
	}
	_ = a.store.PutProject(ctx, project)
}

func queuedProjectMessage(kind OperationKind) string {
	switch kind {
	case OpCreate:
		return statusMessageQueued
	case OpUpdate:
		return statusMessageQueued
	case OpDelete:
		return statusMessageDelQueue
	case OpCI:
		return "queued ci from source webhook"
	case OpDeploy:
		return "queued deployment"
	case OpPromote:
		return "queued promotion"
	default:
		return statusMessageQueued
	}
}

func newProjectOpMsg(
	opID string,
	kind OperationKind,
	projectID string,
	spec ProjectSpec,
	opts opRunOptions,
	now time.Time,
) ProjectOpMsg {
	return ProjectOpMsg{
		OpID:      opID,
		Kind:      kind,
		ProjectID: projectID,
		Spec:      spec,
		DeployEnv: opts.deployEnv,
		FromEnv:   opts.fromEnv,
		ToEnv:     opts.toEnv,
		Err:       "",
		At:        now,
	}
}

func startSubjectForOperation(kind OperationKind) string {
	switch kind {
	case OpCreate:
		return subjectProjectOpStart
	case OpUpdate:
		return subjectProjectOpStart
	case OpDelete:
		return subjectProjectOpStart
	case OpCI:
		return subjectBootstrapDone
	case OpDeploy:
		return subjectDeploymentStart
	case OpPromote:
		return subjectPromotionStart
	default:
		return subjectProjectOpStart
	}
}
