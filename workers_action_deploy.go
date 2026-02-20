package platform

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
	manifests, err := renderKustomizedProjectManifests(spec, imageTag)
	if err != nil {
		return repoBootstrapOutcome{}, err
	}
	renderedArtifacts, err := writeRenderedManifestFiles(
		artifacts,
		msg.ProjectID,
		manifests,
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
	projectID string,
	manifests renderedProjectManifests,
) ([]string, error) {
	manifestFiles := []struct {
		path string
		data string
	}{
		{path: "deploy/deployment.yaml", data: manifests.deployment},
		{path: "deploy/service.yaml", data: manifests.service},
		{path: "deploy/rendered.yaml", data: manifests.rendered},
		{path: "repos/manifests/deployment.yaml", data: manifests.deployment},
		{path: "repos/manifests/service.yaml", data: manifests.service},
		{path: "repos/manifests/kustomization.yaml", data: manifests.kustomization},
		{path: "repos/manifests/rendered.yaml", data: manifests.rendered},
	}
	written := make([]string, 0, len(manifestFiles))
	for _, manifestFile := range manifestFiles {
		artifactPath, err := artifacts.WriteFile(projectID, manifestFile.path, []byte(manifestFile.data))
		if err != nil {
			return written, err
		}
		written = append(written, artifactPath)
	}
	return written, nil
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
