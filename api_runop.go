package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type opRunOptions struct {
	deployEnv string
	fromEnv   string
	toEnv     string
	delivery  DeliveryLifecycle
}

func emptyOpRunOptions() opRunOptions {
	return opRunOptions{
		deployEnv: "",
		fromEnv:   "",
		toEnv:     "",
		delivery: DeliveryLifecycle{
			Stage:       "",
			Environment: "",
			FromEnv:     "",
			ToEnv:       "",
		},
	}
}

func deployOpRunOptions(env string) opRunOptions {
	return opRunOptions{
		deployEnv: env,
		fromEnv:   "",
		toEnv:     "",
		delivery: DeliveryLifecycle{
			Stage:       DeliveryStageDeploy,
			Environment: env,
			FromEnv:     "",
			ToEnv:       "",
		},
	}
}

func transitionOpRunOptions(fromEnv, toEnv string, stage DeliveryStage) opRunOptions {
	return opRunOptions{
		deployEnv: "",
		fromEnv:   fromEnv,
		toEnv:     toEnv,
		delivery: DeliveryLifecycle{
			Stage:       stage,
			Environment: "",
			FromEnv:     fromEnv,
			ToEnv:       toEnv,
		},
	}
}

func (a *API) enqueueOp(
	ctx context.Context,
	kind OperationKind,
	projectID string,
	spec ProjectSpec,
	opts opRunOptions,
) (Operation, error) {
	apiLog := appLoggerForProcess().Source("api")
	opID := newID()
	now := time.Now().UTC()

	op := Operation{
		ID:        opID,
		Kind:      kind,
		ProjectID: projectID,
		Delivery:  opts.delivery,
		Requested: now,
		Finished:  time.Time{},
		Status:    statusMessageQueued,
		Error:     "",
		Steps:     []OpStep{},
	}
	if err := a.store.PutOp(ctx, op); err != nil {
		return Operation{}, fmt.Errorf("persist op: %w", err)
	}
	apiLog.Infof("queued op=%s kind=%s project=%s", opID, kind, projectID)

	if kind != OpDelete {
		a.setQueuedProjectStatus(ctx, opID, kind, projectID, spec, now)
	}

	emitOpBootstrap(a.opEvents, op, "operation accepted and queued")
	emitOpStatus(a.opEvents, op, "queued")

	opMsg := newProjectOpMsg(opID, kind, projectID, spec, opts, now)
	body, _ := json.Marshal(opMsg)
	startSubject := startSubjectForOperation(kind)

	finalizeCtx := context.WithoutCancel(ctx)
	if err := a.nc.Publish(startSubject, body); err != nil {
		_ = finalizeOp(finalizeCtx, a.store, opID, projectID, kind, "error", err.Error())
		apiLog.Errorf("publish failed op=%s kind=%s project=%s: %v", opID, kind, projectID, err)
		return Operation{}, fmt.Errorf("publish op: %w", err)
	}
	apiLog.Debugf("published op=%s subject=%s", opID, startSubject)
	return op, nil
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
	case OpRelease:
		return "queued release"
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
		Delivery:  opts.delivery,
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
	case OpRelease:
		return subjectPromotionStart
	default:
		return subjectProjectOpStart
	}
}
