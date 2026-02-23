package platform

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const (
	promotionStepPlan     = "promoter.plan"
	promotionStepRender   = "promoter.render"
	promotionStepCommit   = "promoter.commit"
	promotionStepFinalize = "promoter.finalize"
)

func promotionWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	res := newWorkerResultMsg("promotion worker starting")

	if msg.Kind != OpPromote && msg.Kind != OpRelease {
		return failPromotionOperation(
			ctx,
			store,
			msg,
			res,
			fmt.Errorf("promotion worker only handles %s and %s operations", OpPromote, OpRelease),
		)
	}

	stageOutcome, err := runPromotionLifecycleStages(ctx, store, artifacts, msg)
	if err != nil {
		res.Artifacts = stageOutcome.artifacts
		return failPromotionOperation(ctx, store, msg, res, err)
	}

	res.Message = stageOutcome.message
	res.Artifacts = stageOutcome.artifacts
	_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "done", "")
	return res, nil
}

type promotionExecutionState struct {
	spec            ProjectSpec
	resolvedFromEnv string
	resolvedToEnv   string
	transition      envTransitionDescriptor
	imageByEnv      map[string]string
	sourceImage     string
	outcome         repoBootstrapOutcome
}

func runPromotionLifecycleStages(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (promotionStageOutcome, error) {
	state := new(promotionExecutionState)
	state.spec = normalizeProjectSpec(msg.Spec)
	state.outcome = newRepoBootstrapOutcome()

	stageOutcome, err := runPromotionStage(
		ctx,
		store,
		msg.OpID,
		promotionStepPlan,
		"validate promotion/release request and source image",
		func() (promotionStageOutcome, error) {
			return runPromotionPlanStage(artifacts, msg, state)
		},
	)
	if err != nil {
		return stageOutcome, err
	}

	stageOutcome, err = runPromotionStage(
		ctx,
		store,
		msg.OpID,
		promotionStepRender,
		"render transition manifests for target environment",
		func() (promotionStageOutcome, error) {
			return runPromotionRenderStage(artifacts, msg, state)
		},
	)
	if err != nil {
		return stageOutcome, err
	}

	stageOutcome, err = runPromotionStage(
		ctx,
		store,
		msg.OpID,
		promotionStepCommit,
		"commit transition manifests to repo",
		func() (promotionStageOutcome, error) {
			return runPromotionCommitStage(ctx, store, artifacts, msg, state)
		},
	)
	if err != nil {
		return stageOutcome, err
	}

	return runPromotionStage(
		ctx,
		store,
		msg.OpID,
		promotionStepFinalize,
		"persist transition release record and finalize promotion",
		func() (promotionStageOutcome, error) {
			return runPromotionFinalizeStage(ctx, store, artifacts, msg, state)
		},
	)
}

func runPromotionPlanStage(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	state *promotionExecutionState,
) (promotionStageOutcome, error) {
	var err error
	state.resolvedFromEnv, state.resolvedToEnv, err = validatePromotionRequestEnvironments(state.spec, msg)
	if err != nil {
		return promotionStageOutcome{}, err
	}
	state.transition = transitionDescriptorForRequest(msg.Kind, msg.Delivery, state.resolvedToEnv)
	if msg.Kind == OpRelease && state.transition.stage != DeliveryStageRelease {
		return promotionStageOutcome{}, fmt.Errorf(
			"release operations require production target environment (got %q)",
			state.resolvedToEnv,
		)
	}
	state.imageByEnv, err = loadManifestImageTags(artifacts, msg.ProjectID, state.spec)
	if err != nil {
		return promotionStageOutcome{}, err
	}
	state.sourceImage, err = resolvePromotionSourceImage(
		artifacts,
		msg.ProjectID,
		state.resolvedFromEnv,
		state.imageByEnv,
	)
	if err != nil {
		return promotionStageOutcome{}, err
	}
	if state.sourceImage == "" {
		return promotionStageOutcome{}, fmt.Errorf(
			"no promoted image found for source environment %q",
			state.resolvedFromEnv,
		)
	}
	return promotionStageOutcome{
		message: fmt.Sprintf(
			"planned %s transition from %s to %s",
			state.transition.commitVerb,
			state.resolvedFromEnv,
			state.resolvedToEnv,
		),
		artifacts: nil,
	}, nil
}

func runPromotionRenderStage(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	state *promotionExecutionState,
) (promotionStageOutcome, error) {
	state.imageByEnv[state.resolvedToEnv] = state.sourceImage
	artifactSets, err := renderTransitionManifests(
		artifacts,
		msg.ProjectID,
		state.spec,
		state.imageByEnv,
		state.resolvedToEnv,
		state.sourceImage,
		state.transition,
		state.resolvedFromEnv,
	)
	state.outcome.artifacts = artifactSets.allArtifacts()
	if err != nil {
		return promotionStageOutcome{
			message:   "",
			artifacts: state.outcome.artifacts,
		}, err
	}
	return promotionStageOutcome{
		message: fmt.Sprintf(
			"rendered %s manifests from %s to %s",
			state.transition.commitVerb,
			state.resolvedFromEnv,
			state.resolvedToEnv,
		),
		artifacts: state.outcome.artifacts,
	}, nil
}

func runPromotionCommitStage(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	state *promotionExecutionState,
) (promotionStageOutcome, error) {
	err := commitEnvironmentTransitionManifestsRepo(
		ctx,
		artifacts,
		msg.ProjectID,
		state.transition,
		state.resolvedFromEnv,
		state.resolvedToEnv,
		msg.OpID,
	)
	if err != nil {
		return promotionStageOutcome{
			message:   "",
			artifacts: state.outcome.artifacts,
		}, err
	}
	updateProjectReadyState(ctx, store, msg, state.spec)
	return promotionStageOutcome{
		message: fmt.Sprintf(
			"committed %s transition manifests",
			state.transition.commitVerb,
		),
		artifacts: state.outcome.artifacts,
	}, nil
}

func runPromotionFinalizeStage(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	state *promotionExecutionState,
) (promotionStageOutcome, error) {
	err := persistTransitionReleaseRecord(
		ctx,
		store,
		artifacts,
		msg,
		state.resolvedFromEnv,
		state.resolvedToEnv,
		state.transition,
	)
	if err != nil {
		return promotionStageOutcome{
			message:   "",
			artifacts: state.outcome.artifacts,
		}, err
	}
	state.outcome.message = fmt.Sprintf(
		"%s manifests from %s to %s",
		state.transition.pastVerb,
		state.resolvedFromEnv,
		state.resolvedToEnv,
	)
	return promotionStageOutcome(state.outcome), nil
}

type promotionStageOutcome struct {
	message   string
	artifacts []string
}

func runPromotionStage(
	ctx context.Context,
	store *Store,
	opID string,
	worker string,
	startMessage string,
	run func() (promotionStageOutcome, error),
) (promotionStageOutcome, error) {
	startedAt := time.Now().UTC()
	_ = markOpStepStart(ctx, store, opID, worker, startedAt, startMessage)

	outcome, err := run()
	endedAt := time.Now().UTC()
	if err != nil {
		_ = markOpStepEnd(
			ctx,
			store,
			opID,
			worker,
			endedAt,
			"",
			err.Error(),
			outcome.artifacts,
		)
		return outcome, err
	}

	_ = markOpStepEnd(
		ctx,
		store,
		opID,
		worker,
		endedAt,
		outcome.message,
		"",
		outcome.artifacts,
	)
	return outcome, nil
}

func failPromotionOperation(
	ctx context.Context,
	store *Store,
	msg ProjectOpMsg,
	res WorkerResultMsg,
	stepErr error,
) (WorkerResultMsg, error) {
	_ = finalizeOp(ctx, store, msg.OpID, msg.ProjectID, msg.Kind, "error", stepErr.Error())
	return res, stepErr
}

func persistTransitionReleaseRecord(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	fromEnv string,
	toEnv string,
	transition envTransitionDescriptor,
) error {
	targetImage, err := readRenderedEnvImageTag(artifacts, msg.ProjectID, toEnv)
	if err != nil {
		return err
	}
	return persistReleaseRecord(
		ctx,
		store,
		ReleaseRecord{
			ID:            "",
			ProjectID:     msg.ProjectID,
			Environment:   toEnv,
			OpID:          msg.OpID,
			OpKind:        msg.Kind,
			DeliveryStage: transition.stage,
			FromEnv:       fromEnv,
			ToEnv:         toEnv,
			Image:         targetImage,
			RenderedPath: filepath.ToSlash(
				filepath.Join(
					transition.artifactDir,
					fmt.Sprintf("%s-to-%s", fromEnv, toEnv),
					"rendered.yaml",
				),
			),
			CreatedAt: time.Now().UTC(),
		},
	)
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
