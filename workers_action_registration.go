package platform

import (
	"context"
	"fmt"
	"time"
)

func registrationWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("registration worker starting")
	_ = markOpStepStart(ctx, store, msg.OpID, "registrar", stepStart, "register app configuration")

	spec := normalizeProjectSpec(msg.Spec)
	outcome := newRepoBootstrapOutcome()
	var err error

	switch msg.Kind {
	case OpCreate, OpUpdate:
		outcome, err = runRegistrationCreateOrUpdate(artifacts, msg, spec)
	case OpDelete:
		outcome, err = runRegistrationDelete(artifacts, msg.ProjectID, msg.OpID)
	case OpCI:
		outcome = repoBootstrapOutcome{
			message:   "registration skipped for ci operation",
			artifacts: nil,
		}
	case OpDeploy, OpPromote:
		outcome = repoBootstrapOutcome{
			message:   "registration skipped for deployment/promotion operation",
			artifacts: nil,
		}
	default:
		err = fmt.Errorf("unknown op kind: %s", msg.Kind)
	}
	if err != nil {
		_ = markOpStepEnd(
			ctx,
			store,
			msg.OpID,
			"registrar",
			time.Now().UTC(),
			"",
			err.Error(),
			outcome.artifacts,
		)
		return res, err
	}

	res.Message = outcome.message
	res.Artifacts = outcome.artifacts
	_ = markOpStepEnd(
		ctx,
		store,
		msg.OpID,
		"registrar",
		time.Now().UTC(),
		res.Message,
		"",
		res.Artifacts,
	)
	return res, nil
}

func runRegistrationCreateOrUpdate(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
) (repoBootstrapOutcome, error) {
	if err := validateProjectSpec(spec); err != nil {
		return newRepoBootstrapOutcome(), err
	}
	_, _ = artifacts.EnsureProjectDir(msg.ProjectID)
	projectYAMLPath, err := artifacts.WriteFile(
		msg.ProjectID,
		"registration/project.yaml",
		renderProjectConfigYAML(spec),
	)
	if err != nil {
		return newRepoBootstrapOutcome(), err
	}
	registrationPath, err := artifacts.WriteFile(
		msg.ProjectID,
		"registration/registration.json",
		mustJSON(map[string]any{
			"project_id": msg.ProjectID,
			"op_id":      msg.OpID,
			"kind":       msg.Kind,
			"registered": time.Now().UTC(),
			"name":       spec.Name,
			"runtime":    spec.Runtime,
		}),
	)
	if err != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: []string{projectYAMLPath},
		}, err
	}
	return repoBootstrapOutcome{
		message:   "project registration upserted",
		artifacts: []string{projectYAMLPath, registrationPath},
	}, nil
}

func runRegistrationDelete(
	artifacts ArtifactStore,
	projectID, opID string,
) (repoBootstrapOutcome, error) {
	deregisterBody := fmt.Appendf(
		nil,
		"deregister requested at %s\nop=%s\n",
		time.Now().UTC().Format(time.RFC3339),
		opID,
	)
	deregisterPath, err := artifacts.WriteFile(
		projectID,
		"registration/deregister.txt",
		deregisterBody,
	)
	if err != nil {
		return newRepoBootstrapOutcome(), err
	}
	return repoBootstrapOutcome{
		message:   "project deregistration staged",
		artifacts: []string{deregisterPath},
	}, nil
}
