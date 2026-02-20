package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func manifestRendererWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("manifest renderer worker starting")
	_ = markOpStepStart(
		ctx,
		store,
		msg.OpID,
		"manifestRenderer",
		stepStart,
		"render kubernetes deployment manifests",
	)

	spec := normalizeProjectSpec(msg.Spec)
	imageTag := fmt.Sprintf("local/%s:%s", safeName(spec.Name), shortID(msg.OpID))
	outcome := newRepoBootstrapOutcome()
	var err error

	switch msg.Kind {
	case OpCreate, OpUpdate, OpCI:
		outcome, err = runManifestRendererApply(ctx, store, artifacts, msg, spec, imageTag)
	case OpDelete:
		outcome, err = runManifestRendererDelete(ctx, store, artifacts, msg)
	default:
		err = fmt.Errorf("unknown op kind: %s", msg.Kind)
	}
	if err != nil {
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
		_ = markOpStepEnd(
			ctx,
			store,
			msg.OpID,
			"manifestRenderer",
			time.Now().UTC(),
			"",
			err.Error(),
			outcome.artifacts,
		)
		return res, err
	}

	_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "done", "")
	res.Message = outcome.message
	res.Artifacts = outcome.artifacts
	_ = markOpStepEnd(
		ctx,
		store,
		msg.OpID,
		"manifestRenderer",
		time.Now().UTC(),
		res.Message,
		"",
		res.Artifacts,
	)
	return res, nil
}

func runManifestRendererApply(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	imageTag string,
) (repoBootstrapOutcome, error) {
	deployment := renderDeploymentManifest(spec, imageTag)
	service := renderServiceManifest(spec)
	renderedArtifacts, err := writeRenderedManifestFiles(
		artifacts,
		msg.ProjectID,
		deployment,
		service,
	)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: renderedArtifacts}, err
	}
	manifestsDir := manifestsRepoDir(artifacts, msg.ProjectID)
	repoErr := ensureLocalGitRepo(ctx, manifestsDir)
	if repoErr != nil {
		return repoBootstrapOutcome{message: "", artifacts: renderedArtifacts}, repoErr
	}
	_, commitErr := gitCommitIfChanged(
		ctx,
		manifestsDir,
		fmt.Sprintf("platform-sync: render manifests (%s)", shortID(msg.OpID)),
	)
	if commitErr != nil {
		return repoBootstrapOutcome{message: "", artifacts: renderedArtifacts}, commitErr
	}
	updateProjectReadyState(ctx, store, msg, spec)
	return repoBootstrapOutcome{
		message:   "rendered kubernetes deployment manifests",
		artifacts: renderedArtifacts,
	}, nil
}

func writeRenderedManifestFiles(
	artifacts ArtifactStore,
	projectID, deployment, service string,
) ([]string, error) {
	a1, err := artifacts.WriteFile(projectID, "deploy/deployment.yaml", []byte(deployment))
	if err != nil {
		return nil, err
	}
	a2, err := artifacts.WriteFile(projectID, "deploy/service.yaml", []byte(service))
	if err != nil {
		return []string{a1}, err
	}
	a3, err := artifacts.WriteFile(projectID, "repos/manifests/deployment.yaml", []byte(deployment))
	if err != nil {
		return []string{a1, a2}, err
	}
	a4, err := artifacts.WriteFile(projectID, "repos/manifests/service.yaml", []byte(service))
	if err != nil {
		return []string{a1, a2, a3}, err
	}
	return []string{a1, a2, a3, a4}, nil
}

func updateProjectReadyState(
	ctx context.Context,
	store *Store,
	msg ProjectOpMsg,
	spec ProjectSpec,
) {
	project, getErr := store.GetProject(ctx, msg.ProjectID)
	if getErr != nil {
		return
	}
	project.Spec = spec
	project.Status = ProjectStatus{
		Phase:      projectPhaseReady,
		UpdatedAt:  time.Now().UTC(),
		LastOpID:   msg.OpID,
		LastOpKind: string(msg.Kind),
		Message:    "ready",
	}
	_ = store.PutProject(ctx, project)
}

func runManifestRendererDelete(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (repoBootstrapOutcome, error) {
	writeDeleteAudit(artifacts, msg.ProjectID, msg.OpID)
	removeErr := artifacts.RemoveProject(msg.ProjectID)
	if removeErr != nil {
		return repoBootstrapOutcome{}, removeErr
	}
	_ = store.DeleteProject(ctx, msg.ProjectID)
	return repoBootstrapOutcome{
		message:   "project deleted and artifacts cleaned",
		artifacts: []string{},
	}, nil
}

func writeDeleteAudit(artifacts ArtifactStore, projectID, opID string) {
	auditDir := filepath.Join(filepath.Dir(artifacts.ProjectDir(projectID)), "_audit")
	_ = os.MkdirAll(auditDir, dirModePrivateRead)
	_ = os.WriteFile(
		filepath.Join(auditDir, fmt.Sprintf("%s.deleted.txt", projectID)),
		fmt.Appendf(
			nil,
			"project=%s deleted at %s op=%s\n",
			projectID,
			time.Now().UTC().Format(time.RFC3339),
			opID,
		),
		fileModePrivate,
	)
}
