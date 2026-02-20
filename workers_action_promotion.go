package platform

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

func promotionWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("promotion worker starting")
	_ = markOpStepStart(
		ctx,
		store,
		msg.OpID,
		"promoter",
		stepStart,
		"promote environment manifests",
	)

	if msg.Kind != OpPromote {
		err := fmt.Errorf("promotion worker only handles %s operations", OpPromote)
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
		_ = markOpStepEnd(ctx, store, msg.OpID, "promoter", time.Now().UTC(), "", err.Error(), nil)
		return res, err
	}

	spec := normalizeProjectSpec(msg.Spec)
	fromEnv := normalizeEnvironmentName(msg.FromEnv)
	toEnv := normalizeEnvironmentName(msg.ToEnv)
	if fromEnv == "" || toEnv == "" {
		err := errors.New("from_env and to_env are required")
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
		_ = markOpStepEnd(ctx, store, msg.OpID, "promoter", time.Now().UTC(), "", err.Error(), nil)
		return res, err
	}
	if fromEnv == toEnv {
		err := errors.New("from_env and to_env must differ")
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
		_ = markOpStepEnd(ctx, store, msg.OpID, "promoter", time.Now().UTC(), "", err.Error(), nil)
		return res, err
	}
	if !isValidEnvironmentName(fromEnv) || !isValidEnvironmentName(toEnv) {
		err := errors.New("from_env and to_env must be valid environment names")
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
		_ = markOpStepEnd(ctx, store, msg.OpID, "promoter", time.Now().UTC(), "", err.Error(), nil)
		return res, err
	}
	if !projectSupportsEnvironment(spec, fromEnv) {
		err := fmt.Errorf("from_env %q is not defined for project", fromEnv)
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
		_ = markOpStepEnd(ctx, store, msg.OpID, "promoter", time.Now().UTC(), "", err.Error(), nil)
		return res, err
	}
	if !projectSupportsEnvironment(spec, toEnv) {
		err := fmt.Errorf("to_env %q is not defined for project", toEnv)
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
		_ = markOpStepEnd(ctx, store, msg.OpID, "promoter", time.Now().UTC(), "", err.Error(), nil)
		return res, err
	}

	outcome, err := runManifestPromotionForEnvironments(
		ctx,
		store,
		artifacts,
		msg,
		spec,
		fromEnv,
		toEnv,
	)
	if err != nil {
		_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", err.Error())
		_ = markOpStepEnd(
			ctx,
			store,
			msg.OpID,
			"promoter",
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
		"promoter",
		time.Now().UTC(),
		res.Message,
		"",
		res.Artifacts,
	)
	return res, nil
}

func runManifestPromotionForEnvironments(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	fromEnv string,
	toEnv string,
) (repoBootstrapOutcome, error) {
	fromEnv = normalizeEnvironmentName(fromEnv)
	toEnv = normalizeEnvironmentName(toEnv)
	if fromEnv == "" || toEnv == "" {
		return repoBootstrapOutcome{}, errors.New("from_env and to_env are required")
	}
	imageByEnv, err := loadManifestImageTags(artifacts, msg.ProjectID, spec)
	if err != nil {
		return repoBootstrapOutcome{}, err
	}
	sourceImage, err := resolvePromotionSourceImage(artifacts, msg.ProjectID, fromEnv, imageByEnv)
	if err != nil {
		return repoBootstrapOutcome{}, err
	}
	if sourceImage == "" {
		return repoBootstrapOutcome{}, fmt.Errorf("no promoted image found for source environment %q", fromEnv)
	}
	imageByEnv[toEnv] = sourceImage

	kustomizeArtifacts, err := writeKustomizeRepoFiles(artifacts, msg.ProjectID, spec, imageByEnv)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: kustomizeArtifacts}, err
	}
	overlayArtifacts, err := forceOverlayImageForEnvironment(
		artifacts,
		msg.ProjectID,
		toEnv,
		sourceImage,
	)
	if err != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: append(kustomizeArtifacts, overlayArtifacts...),
		}, err
	}
	kustomizeArtifacts = append(kustomizeArtifacts, overlayArtifacts...)
	rendered, err := renderEnvironmentManifestsFromRepo(artifacts, msg.ProjectID, toEnv)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: kustomizeArtifacts}, err
	}

	deployArtifacts, err := writeRenderedEnvArtifacts(
		artifacts,
		msg.ProjectID,
		filepath.ToSlash(filepath.Join("deploy", toEnv)),
		rendered,
	)
	if err != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: append(kustomizeArtifacts, deployArtifacts...),
		}, err
	}
	promotionPrefix := filepath.ToSlash(filepath.Join("promotions", fmt.Sprintf("%s-to-%s", fromEnv, toEnv)))
	promotionArtifacts, err := writeRenderedEnvArtifacts(
		artifacts,
		msg.ProjectID,
		promotionPrefix,
		rendered,
	)
	if err != nil {
		all := append(append([]string{}, kustomizeArtifacts...), deployArtifacts...)
		all = append(all, promotionArtifacts...)
		return repoBootstrapOutcome{message: "", artifacts: all}, err
	}
	markerPath, err := artifacts.WriteFile(
		msg.ProjectID,
		filepath.ToSlash(filepath.Join(manifestsRepoOverlaysDir, toEnv, overlayImageMarkerFile)),
		[]byte(sourceImage+"\n"),
	)
	if err != nil {
		all := append(append([]string{}, kustomizeArtifacts...), deployArtifacts...)
		all = append(all, promotionArtifacts...)
		return repoBootstrapOutcome{message: "", artifacts: all}, err
	}
	kustomizeArtifacts = append(kustomizeArtifacts, markerPath)

	commitErr := commitPromotionManifestsRepo(ctx, artifacts, msg.ProjectID, fromEnv, toEnv, msg.OpID)
	if commitErr != nil {
		all := append(append([]string{}, kustomizeArtifacts...), deployArtifacts...)
		all = append(all, promotionArtifacts...)
		return repoBootstrapOutcome{message: "", artifacts: all}, commitErr
	}

	updateProjectReadyState(ctx, store, msg, spec)
	allArtifacts := append(append([]string{}, kustomizeArtifacts...), deployArtifacts...)
	allArtifacts = append(allArtifacts, promotionArtifacts...)
	return repoBootstrapOutcome{
		message:   fmt.Sprintf("promoted manifests from %s to %s", fromEnv, toEnv),
		artifacts: uniqueSorted(allArtifacts),
	}, nil
}

func resolvePromotionSourceImage(
	artifacts ArtifactStore,
	projectID string,
	fromEnv string,
	imageByEnv map[string]string,
) (string, error) {
	sourceImage, err := readRenderedEnvImageTag(artifacts, projectID, fromEnv)
	if err != nil {
		return "", err
	}
	if sourceImage != "" {
		return sourceImage, nil
	}
	return imageByEnv[fromEnv], nil
}

func commitPromotionManifestsRepo(
	ctx context.Context,
	artifacts ArtifactStore,
	projectID string,
	fromEnv string,
	toEnv string,
	opID string,
) error {
	manifestsDir := manifestsRepoDir(artifacts, projectID)
	if err := ensureLocalGitRepo(ctx, manifestsDir); err != nil {
		return err
	}
	_, err := gitCommitIfChanged(
		ctx,
		manifestsDir,
		fmt.Sprintf(
			"platform-sync: promote manifests %s->%s (%s)",
			fromEnv,
			toEnv,
			shortID(opID),
		),
	)
	return err
}

func forceOverlayImageForEnvironment(
	artifacts ArtifactStore,
	projectID string,
	env string,
	image string,
) ([]string, error) {
	overlayDir := filepath.ToSlash(filepath.Join(manifestsRepoOverlaysDir, env))
	files := []struct {
		path string
		data string
	}{
		{
			path: filepath.ToSlash(filepath.Join(overlayDir, manifestFileKustomization)),
			data: renderOverlayKustomizationManifest(image),
		},
		{
			path: filepath.ToSlash(filepath.Join(overlayDir, overlayImageMarkerFile)),
			data: image + "\n",
		},
	}
	written := make([]string, 0, len(files))
	for _, file := range files {
		artifactPath, err := artifacts.WriteFile(projectID, file.path, []byte(file.data))
		if err != nil {
			return written, err
		}
		written = append(written, artifactPath)
	}
	return written, nil
}
