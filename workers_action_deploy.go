package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	manifestsRepoBaseDir           = "repos/manifests/base"
	manifestsRepoOverlaysDir       = "repos/manifests/overlays"
	manifestsRepoRootKustomization = "repos/manifests/kustomization.yaml"
	overlayDeploymentPatchFile     = "deployment-patch.yaml"
	overlayImageMarkerFile         = "image.txt"
)

func manifestRendererWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	workerLog := appLoggerForProcess().Source("manifestRenderer")
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("manifest renderer worker starting")
	_ = markOpStepStart(
		ctx,
		store,
		msg.OpID,
		"manifestRenderer",
		stepStart,
		"render and deploy dev manifests from kustomize overlays",
	)

	spec := normalizeProjectSpec(msg.Spec)
	imageTag := fmt.Sprintf("local/%s:%s", safeName(spec.Name), shortID(msg.OpID))
	outcome := newRepoBootstrapOutcome()
	var err error

	switch msg.Kind {
	case OpCreate, OpUpdate, OpCI:
		outcome, err = runManifestApplyForEnvironment(
			ctx,
			store,
			artifacts,
			msg,
			spec,
			imageTag,
			defaultDeployEnvironment,
		)
	case OpDelete:
		outcome, err = runManifestRendererDelete(ctx, store, artifacts, msg)
	case OpDeploy, OpPromote, OpRelease, OpRollback:
		err = fmt.Errorf("manifest renderer does not handle %s operations", msg.Kind)
	default:
		err = fmt.Errorf("unknown op kind: %s", msg.Kind)
	}
	if err != nil {
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
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
		if msg.Kind == OpCI {
			stateErr := finalizeSourceCommitPendingOp(artifacts, msg.ProjectID, msg.OpID, false)
			if stateErr != nil {
				workerLog.Warnf(
					"project=%s op=%s persist failed ci pending state: %v",
					msg.ProjectID,
					msg.OpID,
					stateErr,
				)
			}
		}
		return res, err
	}

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
	_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "done", "")
	if msg.Kind == OpCI {
		stateErr := finalizeSourceCommitPendingOp(artifacts, msg.ProjectID, msg.OpID, true)
		if stateErr != nil {
			workerLog.Warnf(
				"project=%s op=%s persist successful ci commit state: %v",
				msg.ProjectID,
				msg.OpID,
				stateErr,
			)
		}
	}
	return res, nil
}

func deploymentWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("deployment worker starting")
	_ = markOpStepStart(
		ctx,
		store,
		msg.OpID,
		"deployer",
		stepStart,
		"deploy manifests for a single environment",
	)

	if msg.Kind != OpDeploy {
		return failDeploymentStep(
			ctx,
			store,
			msg,
			res,
			fmt.Errorf("deployment worker only handles %s operations", OpDeploy),
			nil,
		)
	}

	targetEnv := resolveDeployEnvironment(msg.DeployEnv)
	if targetEnv != defaultDeployEnvironment {
		return failDeploymentStep(
			ctx,
			store,
			msg,
			res,
			fmt.Errorf(
				"deployment environment %q not supported; use promotion/release for higher environments",
				targetEnv,
			),
			nil,
		)
	}

	imageTag, err := readBuildImageTagForDeployment(artifacts, msg.ProjectID)
	if err != nil {
		return failDeploymentStep(ctx, store, msg, res, err, nil)
	}

	spec := normalizeProjectSpec(msg.Spec)
	outcome, err := runManifestApplyForEnvironment(
		ctx,
		store,
		artifacts,
		msg,
		spec,
		imageTag,
		targetEnv,
	)
	if err != nil {
		return failDeploymentStep(ctx, store, msg, res, err, outcome.artifacts)
	}
	if err = persistDeployReleaseRecord(ctx, store, artifacts, msg, targetEnv, imageTag); err != nil {
		return failDeploymentStep(ctx, store, msg, res, err, outcome.artifacts)
	}

	res.Message = outcome.message
	res.Artifacts = outcome.artifacts
	_ = markOpStepEnd(
		ctx,
		store,
		msg.OpID,
		"deployer",
		time.Now().UTC(),
		res.Message,
		"",
		res.Artifacts,
	)
	_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "done", "")
	return res, nil
}

func failDeploymentStep(
	ctx context.Context,
	store *Store,
	msg ProjectOpMsg,
	res WorkerResultMsg,
	stepErr error,
	artifacts []string,
) (WorkerResultMsg, error) {
	_ = markOpStepEnd(
		ctx,
		store,
		msg.OpID,
		"deployer",
		time.Now().UTC(),
		"",
		stepErr.Error(),
		artifacts,
	)
	_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", stepErr.Error())
	return res, stepErr
}

func persistDeployReleaseRecord(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	targetEnv string,
	fallbackImage string,
) error {
	deployedImage, err := readRenderedEnvImageTag(artifacts, msg.ProjectID, targetEnv)
	if err != nil {
		return err
	}
	if deployedImage == "" {
		deployedImage = strings.TrimSpace(fallbackImage)
	}
	return persistReleaseRecord(
		ctx,
		store,
		ReleaseRecord{
			ID:                    "",
			ProjectID:             msg.ProjectID,
			Environment:           targetEnv,
			OpID:                  msg.OpID,
			OpKind:                msg.Kind,
			DeliveryStage:         DeliveryStageDeploy,
			FromEnv:               "",
			ToEnv:                 targetEnv,
			Image:                 deployedImage,
			RenderedPath:          filepath.ToSlash(filepath.Join("deploy", targetEnv, "rendered.yaml")),
			ConfigPath:            filepath.ToSlash(filepath.Join("deploy", targetEnv, "deployment.yaml")),
			RollbackSafe:          rollbackSafeDefaultPtr(),
			RollbackSourceRelease: "",
			RollbackScope:         "",
			CreatedAt:             time.Now().UTC(),
		},
	)
}

func runManifestApplyForEnvironment(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	imageTag string,
	targetEnv string,
) (repoBootstrapOutcome, error) {
	targetEnv = normalizeEnvironmentName(targetEnv)
	if targetEnv == "" {
		targetEnv = defaultDeployEnvironment
	}
	if !isValidEnvironmentName(targetEnv) {
		return repoBootstrapOutcome{}, fmt.Errorf("invalid deployment environment %q", targetEnv)
	}

	imageByEnv, err := loadManifestImageTags(artifacts, msg.ProjectID, spec)
	if err != nil {
		return repoBootstrapOutcome{}, err
	}
	imageByEnv[targetEnv] = strings.TrimSpace(imageTag)

	kustomizeArtifacts, err := writeKustomizeRepoFiles(artifacts, msg.ProjectID, spec, imageByEnv)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: kustomizeArtifacts}, err
	}
	rendered, err := renderEnvironmentManifestsFromRepo(artifacts, msg.ProjectID, targetEnv)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: kustomizeArtifacts}, err
	}
	deployArtifacts, err := writeRenderedEnvArtifacts(
		artifacts,
		msg.ProjectID,
		filepath.ToSlash(filepath.Join("deploy", targetEnv)),
		rendered,
	)
	if err != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: append(kustomizeArtifacts, deployArtifacts...),
		}, err
	}

	manifestsDir := manifestsRepoDir(artifacts, msg.ProjectID)
	repoErr := ensureLocalGitRepo(ctx, manifestsDir)
	if repoErr != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: append(kustomizeArtifacts, deployArtifacts...),
		}, repoErr
	}
	_, commitErr := gitCommitIfChanged(
		ctx,
		manifestsDir,
		fmt.Sprintf("platform-sync: deploy %s manifests (%s)", targetEnv, shortID(msg.OpID)),
	)
	if commitErr != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: append(kustomizeArtifacts, deployArtifacts...),
		}, commitErr
	}

	updateProjectReadyState(ctx, store, msg, spec)
	allArtifacts := append([]string{}, kustomizeArtifacts...)
	allArtifacts = append(allArtifacts, deployArtifacts...)
	return repoBootstrapOutcome{
		message:   fmt.Sprintf("deployed kustomize manifests for %s environment", targetEnv),
		artifacts: uniqueSorted(allArtifacts),
	}, nil
}

func writeKustomizeRepoFiles(
	artifacts ArtifactStore,
	projectID string,
	spec ProjectSpec,
	imageByEnv map[string]string,
) ([]string, error) {
	spec = normalizeProjectSpec(spec)
	files := []struct {
		path string
		data string
	}{
		{
			path: filepath.ToSlash(filepath.Join(manifestsRepoBaseDir, manifestFileDeployment)),
			data: renderBaseDeploymentManifest(spec),
		},
		{
			path: filepath.ToSlash(filepath.Join(manifestsRepoBaseDir, manifestFileService)),
			data: renderServiceManifest(spec),
		},
		{
			path: filepath.ToSlash(filepath.Join(manifestsRepoBaseDir, manifestFileKustomization)),
			data: renderBaseKustomizationManifest(),
		},
		{
			path: manifestsRepoRootKustomization,
			data: renderRootKustomizationManifest(defaultDeployEnvironment),
		},
	}

	envs := desiredManifestEnvironments(spec)
	for _, env := range envs {
		envImage := strings.TrimSpace(imageByEnv[env])
		if envImage == "" {
			envImage = defaultManifestImage(spec)
		}
		overlayDir := filepath.ToSlash(filepath.Join(manifestsRepoOverlaysDir, env))
		files = append(files,
			struct {
				path string
				data string
			}{
				path: filepath.ToSlash(filepath.Join(overlayDir, manifestFileKustomization)),
				data: renderOverlayKustomizationManifest(envImage),
			},
			struct {
				path string
				data string
			}{
				path: filepath.ToSlash(filepath.Join(overlayDir, overlayDeploymentPatchFile)),
				data: renderDeploymentEnvPatch(spec, env),
			},
			struct {
				path string
				data string
			}{
				path: filepath.ToSlash(filepath.Join(overlayDir, overlayImageMarkerFile)),
				data: envImage + "\n",
			},
		)
	}

	written := make([]string, 0, len(files))
	for _, file := range files {
		artifactPath, err := artifacts.WriteFile(projectID, file.path, []byte(file.data))
		if err != nil {
			return written, err
		}
		written = append(written, artifactPath)
	}
	return uniqueSorted(written), nil
}

func renderRootKustomizationManifest(defaultEnv string) string {
	return fmt.Sprintf(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - overlays/%s
`, defaultEnv)
}

func desiredManifestEnvironments(spec ProjectSpec) []string {
	spec = normalizeProjectSpec(spec)
	seen := map[string]struct{}{defaultDeployEnvironment: {}}
	for env := range spec.Environments {
		seen[normalizeEnvironmentName(env)] = struct{}{}
	}
	envs := make([]string, 0, len(seen))
	for env := range seen {
		if env == "" {
			continue
		}
		envs = append(envs, env)
	}
	slices.Sort(envs)
	return envs
}

func defaultManifestImage(spec ProjectSpec) string {
	spec = normalizeProjectSpec(spec)
	return fmt.Sprintf("local/%s:latest", safeName(spec.Name))
}

func loadManifestImageTags(
	artifacts ArtifactStore,
	projectID string,
	spec ProjectSpec,
) (map[string]string, error) {
	envs := desiredManifestEnvironments(spec)
	if len(envs) == 0 {
		envs = []string{defaultDeployEnvironment}
	}
	imageByEnv := make(map[string]string, len(envs))
	fallback := defaultManifestImage(spec)
	for _, env := range envs {
		relPath := filepath.ToSlash(filepath.Join(manifestsRepoOverlaysDir, env, overlayImageMarkerFile))
		raw, err := artifacts.ReadFile(projectID, relPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				imageByEnv[env] = fallback
				continue
			}
			return nil, err
		}
		trimmed := strings.TrimSpace(string(raw))
		if trimmed == "" {
			trimmed = fallback
		}
		imageByEnv[env] = trimmed
	}
	return imageByEnv, nil
}

func renderEnvironmentManifestsFromRepo(
	artifacts ArtifactStore,
	projectID string,
	env string,
) (renderedProjectManifests, error) {
	env = normalizeEnvironmentName(env)
	overlayPath := filepath.Join(manifestsRepoDir(artifacts, projectID), "overlays", env)
	rendered, err := runKustomizeBuildAtPath(overlayPath)
	if err != nil {
		return renderedProjectManifests{}, err
	}
	deployment, service, err := splitRenderedManifests(rendered)
	if err != nil {
		return renderedProjectManifests{}, err
	}
	return renderedProjectManifests{
		deployment:    deployment,
		service:       service,
		kustomization: "",
		rendered:      string(rendered),
	}, nil
}

func writeRenderedEnvArtifacts(
	artifacts ArtifactStore,
	projectID string,
	prefix string,
	rendered renderedProjectManifests,
) ([]string, error) {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	files := []struct {
		path string
		data string
	}{
		{path: filepath.ToSlash(filepath.Join(prefix, manifestFileDeployment)), data: rendered.deployment},
		{path: filepath.ToSlash(filepath.Join(prefix, manifestFileService)), data: rendered.service},
		{path: filepath.ToSlash(filepath.Join(prefix, "rendered.yaml")), data: rendered.rendered},
	}
	written := make([]string, 0, len(files))
	for _, file := range files {
		artifactPath, err := artifacts.WriteFile(projectID, file.path, []byte(file.data))
		if err != nil {
			return written, err
		}
		written = append(written, artifactPath)
	}
	return uniqueSorted(written), nil
}

func resolveDeployEnvironment(raw string) string {
	env := normalizeEnvironmentName(raw)
	if env == "" {
		return defaultDeployEnvironment
	}
	return env
}

func readBuildImageTagForDeployment(artifacts ArtifactStore, projectID string) (string, error) {
	raw, err := artifacts.ReadFile(projectID, imageBuildTagPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("no build image found; run create/update/ci before deploy")
		}
		return "", err
	}
	imageTag := strings.TrimSpace(string(raw))
	if imageTag == "" {
		return "", errors.New("no build image found; run create/update/ci before deploy")
	}
	return imageTag, nil
}

func readRenderedEnvImageTag(
	artifacts ArtifactStore,
	projectID string,
	env string,
) (string, error) {
	env = normalizeEnvironmentName(env)
	if env == "" {
		return "", nil
	}
	path := filepath.ToSlash(filepath.Join("deploy", env, manifestFileDeployment))
	raw, err := artifacts.ReadFile(projectID, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		image, ok := strings.CutPrefix(trimmed, "image:")
		if ok {
			return strings.TrimSpace(image), nil
		}
	}
	return "", nil
}

func updateProjectReadyState(
	ctx context.Context,
	store *Store,
	msg ProjectOpMsg,
	spec ProjectSpec,
) {
	if store == nil {
		return
	}
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

func persistReleaseRecord(ctx context.Context, store *Store, release ReleaseRecord) error {
	if store == nil {
		return nil
	}
	_, err := store.PutRelease(ctx, release)
	return err
}

func rollbackSafeDefaultPtr() *bool {
	safe := true
	return &safe
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
	if store != nil {
		_ = store.DeleteProject(ctx, msg.ProjectID)
	}
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
