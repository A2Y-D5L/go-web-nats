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
		"promote/release environment manifests",
	)

	if msg.Kind != OpPromote && msg.Kind != OpRelease {
		return failPromotionStep(
			ctx,
			store,
			msg,
			res,
			fmt.Errorf("promotion worker only handles %s and %s operations", OpPromote, OpRelease),
			nil,
		)
	}

	spec := normalizeProjectSpec(msg.Spec)
	resolvedFromEnv, resolvedToEnv, err := validatePromotionRequestEnvironments(spec, msg)
	if err != nil {
		return failPromotionStep(ctx, store, msg, res, err, nil)
	}
	transition := transitionDescriptorForRequest(msg.Kind, msg.Delivery, resolvedToEnv)
	if msg.Kind == OpRelease && transition.stage != DeliveryStageRelease {
		return failPromotionStep(
			ctx,
			store,
			msg,
			res,
			fmt.Errorf("release operations require production target environment (got %q)", resolvedToEnv),
			nil,
		)
	}

	outcome, err := runManifestPromotionForEnvironments(
		ctx,
		store,
		artifacts,
		msg,
		spec,
		resolvedFromEnv,
		resolvedToEnv,
	)
	if err != nil {
		return failPromotionStep(ctx, store, msg, res, err, outcome.artifacts)
	}

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
	_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "done", "")
	return res, nil
}

func failPromotionStep(
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
		"promoter",
		time.Now().UTC(),
		"",
		stepErr.Error(),
		artifacts,
	)
	_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", stepErr.Error())
	return res, stepErr
}

func validatePromotionRequestEnvironments(
	spec ProjectSpec,
	msg ProjectOpMsg,
) (string, string, error) {
	fromEnv := normalizeEnvironmentName(msg.FromEnv)
	toEnv := normalizeEnvironmentName(msg.ToEnv)
	switch {
	case fromEnv == "" || toEnv == "":
		return "", "", errors.New("from_env and to_env are required")
	case fromEnv == toEnv:
		return "", "", errors.New("from_env and to_env must differ")
	case !isValidEnvironmentName(fromEnv) || !isValidEnvironmentName(toEnv):
		return "", "", errors.New("from_env and to_env must be valid environment names")
	}

	resolvedFromEnv, ok := resolveProjectEnvironmentName(spec, fromEnv)
	if !ok {
		return "", "", fmt.Errorf("from_env %q is not defined for project", fromEnv)
	}
	resolvedToEnv, ok := resolveProjectEnvironmentName(spec, toEnv)
	if !ok {
		return "", "", fmt.Errorf("to_env %q is not defined for project", toEnv)
	}
	if resolvedFromEnv == resolvedToEnv {
		return "", "", errors.New("from_env and to_env must differ")
	}
	return resolvedFromEnv, resolvedToEnv, nil
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
	transition := transitionDescriptorForRequest(msg.Kind, msg.Delivery, toEnv)
	if transition.stage == DeliveryStageRelease && !isProductionEnvironment(toEnv) {
		return repoBootstrapOutcome{}, fmt.Errorf("release target environment must be production (got %q)", toEnv)
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

	artifactSets, err := renderTransitionManifests(
		artifacts,
		msg.ProjectID,
		spec,
		imageByEnv,
		toEnv,
		sourceImage,
		transition,
		fromEnv,
	)
	if err != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: artifactSets.allArtifacts(),
		}, err
	}

	commitErr := commitEnvironmentTransitionManifestsRepo(
		ctx,
		artifacts,
		msg.ProjectID,
		transition,
		fromEnv,
		toEnv,
		msg.OpID,
	)
	if commitErr != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: artifactSets.allArtifacts(),
		}, commitErr
	}

	updateProjectReadyState(ctx, store, msg, spec)
	allArtifacts := artifactSets.allArtifacts()
	return repoBootstrapOutcome{
		message:   fmt.Sprintf("%s manifests from %s to %s", transition.pastVerb, fromEnv, toEnv),
		artifacts: allArtifacts,
	}, nil
}

type transitionArtifactSets struct {
	kustomizeArtifacts  []string
	deployArtifacts     []string
	transitionArtifacts []string
}

func newTransitionArtifactSets() transitionArtifactSets {
	return transitionArtifactSets{
		kustomizeArtifacts:  nil,
		deployArtifacts:     nil,
		transitionArtifacts: nil,
	}
}

func (s transitionArtifactSets) allArtifacts() []string {
	all := append([]string{}, s.kustomizeArtifacts...)
	all = append(all, s.deployArtifacts...)
	all = append(all, s.transitionArtifacts...)
	return uniqueSorted(all)
}

func renderTransitionManifests(
	artifacts ArtifactStore,
	projectID string,
	spec ProjectSpec,
	imageByEnv map[string]string,
	toEnv string,
	sourceImage string,
	transition envTransitionDescriptor,
	fromEnv string,
) (transitionArtifactSets, error) {
	sets := newTransitionArtifactSets()

	kustomizeArtifacts, err := writeKustomizeRepoFiles(artifacts, projectID, spec, imageByEnv)
	sets.kustomizeArtifacts = kustomizeArtifacts
	if err != nil {
		return sets, err
	}
	overlayArtifacts, err := forceOverlayImageForEnvironment(artifacts, projectID, toEnv, sourceImage)
	sets.kustomizeArtifacts = append(sets.kustomizeArtifacts, overlayArtifacts...)
	if err != nil {
		return sets, err
	}
	rendered, err := renderEnvironmentManifestsFromRepo(artifacts, projectID, toEnv)
	if err != nil {
		return sets, err
	}

	sets.deployArtifacts, err = writeRenderedEnvArtifacts(
		artifacts,
		projectID,
		filepath.ToSlash(filepath.Join("deploy", toEnv)),
		rendered,
	)
	if err != nil {
		return sets, err
	}

	transitionPrefix := filepath.ToSlash(
		filepath.Join(transition.artifactDir, fmt.Sprintf("%s-to-%s", fromEnv, toEnv)),
	)
	sets.transitionArtifacts, err = writeRenderedEnvArtifacts(
		artifacts,
		projectID,
		transitionPrefix,
		rendered,
	)
	if err != nil {
		return sets, err
	}

	markerPath, err := artifacts.WriteFile(
		projectID,
		filepath.ToSlash(filepath.Join(manifestsRepoOverlaysDir, toEnv, overlayImageMarkerFile)),
		[]byte(sourceImage+"\n"),
	)
	if err != nil {
		return sets, err
	}
	sets.kustomizeArtifacts = append(sets.kustomizeArtifacts, markerPath)
	return sets, nil
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

func commitEnvironmentTransitionManifestsRepo(
	ctx context.Context,
	artifacts ArtifactStore,
	projectID string,
	transition envTransitionDescriptor,
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
			"platform-sync: %s manifests %s->%s (%s)",
			transition.commitVerb,
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

type envTransitionDescriptor struct {
	stage       DeliveryStage
	artifactDir string
	commitVerb  string
	pastVerb    string
}

func transitionDescriptorForRequest(
	kind OperationKind,
	delivery DeliveryLifecycle,
	toEnv string,
) envTransitionDescriptor {
	stage := delivery.Stage
	switch {
	case stage == DeliveryStageRelease:
	case stage == DeliveryStagePromote:
	case kind == OpRelease:
		stage = DeliveryStageRelease
	case isProductionEnvironment(toEnv):
		stage = DeliveryStageRelease
	default:
		stage = DeliveryStagePromote
	}
	if stage == DeliveryStageRelease {
		return envTransitionDescriptor{
			stage:       stage,
			artifactDir: "releases",
			commitVerb:  "release",
			pastVerb:    "released",
		}
	}
	return envTransitionDescriptor{
		stage:       DeliveryStagePromote,
		artifactDir: "promotions",
		commitVerb:  "promote",
		pastVerb:    "promoted",
	}
}
