package platform

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
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

	if msg.Kind != OpPromote && msg.Kind != OpRelease && msg.Kind != OpRollback {
		return failPromotionOperation(
			ctx,
			store,
			msg,
			res,
			fmt.Errorf("promotion worker only handles %s, %s, and %s operations", OpPromote, OpRelease, OpRollback),
		)
	}

	var (
		stageOutcome promotionStageOutcome
		err          error
	)
	if msg.Kind == OpRollback {
		stageOutcome, err = runRollbackLifecycleStages(ctx, store, artifacts, msg)
	} else {
		stageOutcome, err = runPromotionLifecycleStages(ctx, store, artifacts, msg)
	}
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

type rollbackExecutionState struct {
	spec          ProjectSpec
	targetEnv     string
	scope         RollbackScope
	sourceRelease ReleaseRecord
	sourceImage   string
	configVars    map[string]string
	rendered      renderedProjectManifests
	rollbackDir   string
	artifactSets  transitionArtifactSets
	outcome       repoBootstrapOutcome
}

func runRollbackLifecycleStages(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (promotionStageOutcome, error) {
	state := &rollbackExecutionState{
		spec:          normalizeProjectSpec(msg.Spec),
		targetEnv:     "",
		scope:         "",
		sourceRelease: zeroReleaseRecord(),
		sourceImage:   "",
		configVars:    map[string]string{},
		rendered:      zeroRenderedProjectManifests(),
		rollbackDir:   "",
		artifactSets:  newTransitionArtifactSets(),
		outcome:       newRepoBootstrapOutcome(),
	}

	stageOutcome, err := runPromotionStage(
		ctx,
		store,
		msg.OpID,
		promotionStepPlan,
		"validate rollback request and selected release snapshot",
		func() (promotionStageOutcome, error) {
			return runRollbackPlanStage(ctx, store, artifacts, msg, state)
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
		"render rollback manifests for target environment",
		func() (promotionStageOutcome, error) {
			return runRollbackRenderStage(artifacts, msg, state)
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
		"commit rollback manifests to repo",
		func() (promotionStageOutcome, error) {
			return runRollbackCommitStage(ctx, store, artifacts, msg, state)
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
		"persist rollback release record and finalize rollback",
		func() (promotionStageOutcome, error) {
			return runRollbackFinalizeStage(ctx, store, artifacts, msg, state)
		},
	)
}

func runRollbackPlanStage(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	state *rollbackExecutionState,
) (promotionStageOutcome, error) {
	if err := applyRollbackPlanRequest(msg, state); err != nil {
		return promotionStageOutcome{}, err
	}
	release, err := loadRollbackPlanSourceRelease(ctx, store, msg, state.targetEnv)
	if err != nil {
		return promotionStageOutcome{}, err
	}
	state.sourceRelease = release
	err = hydrateRollbackPlanArtifacts(artifacts, msg, state)
	if err != nil {
		return promotionStageOutcome{}, err
	}

	return promotionStageOutcome{
		message: fmt.Sprintf(
			"planned rollback of %s using release %s with scope %s",
			state.targetEnv,
			shortID(state.sourceRelease.ID),
			state.scope,
		),
		artifacts: nil,
	}, nil
}

func applyRollbackPlanRequest(msg ProjectOpMsg, state *rollbackExecutionState) error {
	scope, ok := normalizeRollbackScope(msg.RollbackScope)
	if !ok {
		return fmt.Errorf(
			"rollback scope must be one of %q, %q, %q",
			RollbackScopeCodeOnly,
			RollbackScopeCodeAndConfig,
			RollbackScopeFullState,
		)
	}
	state.scope = scope

	targetEnv := normalizeEnvironmentName(msg.RollbackEnv)
	if targetEnv == "" {
		return errors.New("rollback environment is required")
	}
	resolvedEnv, envOK := resolveProjectEnvironmentName(state.spec, targetEnv)
	if !envOK {
		return fmt.Errorf("rollback environment %q is not defined for project", targetEnv)
	}
	state.targetEnv = resolvedEnv
	state.rollbackDir = filepath.ToSlash(
		filepath.Join("rollbacks", state.targetEnv, shortID(msg.OpID)),
	)
	return nil
}

func loadRollbackPlanSourceRelease(
	ctx context.Context,
	store *Store,
	msg ProjectOpMsg,
	targetEnv string,
) (ReleaseRecord, error) {
	releaseID := strings.TrimSpace(msg.RollbackReleaseID)
	if releaseID == "" {
		return ReleaseRecord{}, errors.New("rollback release_id is required")
	}
	release, err := store.GetRelease(ctx, releaseID)
	if err != nil {
		return ReleaseRecord{}, fmt.Errorf("failed to read rollback release: %w", err)
	}
	release = normalizeReleaseRecord(release)
	if strings.TrimSpace(release.ProjectID) != strings.TrimSpace(msg.ProjectID) {
		return ReleaseRecord{}, errors.New("rollback release does not belong to project")
	}
	if normalizeEnvironmentName(release.Environment) != targetEnv {
		return ReleaseRecord{}, fmt.Errorf(
			"rollback release environment %q does not match target %q",
			release.Environment,
			targetEnv,
		)
	}
	if release.RollbackSafe != nil && !*release.RollbackSafe && !msg.RollbackOverride {
		return ReleaseRecord{}, errors.New(
			"rollback blocked: selected release is marked rollback_safe=false",
		)
	}
	return release, nil
}

func hydrateRollbackPlanArtifacts(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	state *rollbackExecutionState,
) error {
	sourceImage, err := resolveRollbackReleaseImage(
		artifacts,
		msg.ProjectID,
		state.sourceRelease,
	)
	if err != nil {
		return err
	}
	if sourceImage == "" {
		return errors.New("rollback release has no image snapshot")
	}
	state.sourceImage = sourceImage
	return applyRollbackScopeSnapshots(artifacts, msg.ProjectID, state)
}

func applyRollbackScopeSnapshots(
	artifacts ArtifactStore,
	projectID string,
	state *rollbackExecutionState,
) error {
	if state.scope == RollbackScopeCodeOnly {
		return nil
	}
	if err := applyRollbackScopeConfigSnapshot(artifacts, projectID, state); err != nil {
		return err
	}
	if state.scope != RollbackScopeFullState {
		return nil
	}
	renderedSnapshot, err := readRollbackRenderedSnapshot(
		artifacts,
		projectID,
		state.sourceRelease,
	)
	if err != nil {
		return err
	}
	state.rendered = renderedSnapshot
	return nil
}

func applyRollbackScopeConfigSnapshot(
	artifacts ArtifactStore,
	projectID string,
	state *rollbackExecutionState,
) error {
	configSnapshot, err := readRollbackReleaseConfigSnapshot(
		artifacts,
		projectID,
		state.sourceRelease,
	)
	if err != nil {
		return err
	}
	if len(configSnapshot) == 0 {
		return errors.New("rollback config snapshot is required for selected scope")
	}
	state.configVars = parseDeploymentEnvVars(configSnapshot)
	state.spec = applyRollbackConfigToSpec(
		state.spec,
		state.targetEnv,
		state.configVars,
	)
	return nil
}

func zeroReleaseRecord() ReleaseRecord {
	return ReleaseRecord{
		ID:                    "",
		ProjectID:             "",
		Environment:           "",
		OpID:                  "",
		OpKind:                "",
		DeliveryStage:         "",
		FromEnv:               "",
		ToEnv:                 "",
		Image:                 "",
		RenderedPath:          "",
		ConfigPath:            "",
		RollbackSafe:          nil,
		RollbackSourceRelease: "",
		RollbackScope:         "",
		CreatedAt:             time.Time{},
	}
}

func zeroRenderedProjectManifests() renderedProjectManifests {
	return renderedProjectManifests{
		deployment:    "",
		service:       "",
		kustomization: "",
		rendered:      "",
	}
}

func runRollbackRenderStage(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	state *rollbackExecutionState,
) (promotionStageOutcome, error) {
	var (
		sets transitionArtifactSets
		err  error
	)

	if state.scope == RollbackScopeFullState {
		sets, err = renderRollbackFullStateArtifacts(artifacts, msg, state)
	} else {
		sets, err = renderRollbackFromCurrentSpecArtifacts(artifacts, msg, state)
	}
	state.artifactSets = sets
	state.outcome.artifacts = sets.allArtifacts()
	if err != nil {
		return promotionStageOutcome{
			message:   "",
			artifacts: state.outcome.artifacts,
		}, err
	}

	return promotionStageOutcome{
		message: fmt.Sprintf(
			"rendered rollback manifests for %s (%s)",
			state.targetEnv,
			state.scope,
		),
		artifacts: state.outcome.artifacts,
	}, nil
}

func runRollbackCommitStage(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	state *rollbackExecutionState,
) (promotionStageOutcome, error) {
	if err := commitRollbackManifestsRepo(
		ctx,
		artifacts,
		msg.ProjectID,
		state.targetEnv,
		msg.OpID,
		state.scope,
		state.sourceRelease.ID,
	); err != nil {
		return promotionStageOutcome{
			message:   "",
			artifacts: state.outcome.artifacts,
		}, err
	}
	updateProjectReadyState(ctx, store, msg, state.spec)
	return promotionStageOutcome{
		message: fmt.Sprintf(
			"committed rollback manifests for %s",
			state.targetEnv,
		),
		artifacts: state.outcome.artifacts,
	}, nil
}

func runRollbackFinalizeStage(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	state *rollbackExecutionState,
) (promotionStageOutcome, error) {
	image, err := readRenderedEnvImageTag(artifacts, msg.ProjectID, state.targetEnv)
	if err != nil {
		return promotionStageOutcome{
			message:   "",
			artifacts: state.outcome.artifacts,
		}, err
	}
	if strings.TrimSpace(image) == "" {
		image = strings.TrimSpace(state.sourceImage)
	}
	if err = persistReleaseRecord(
		ctx,
		store,
		ReleaseRecord{
			ID:                    "",
			ProjectID:             msg.ProjectID,
			Environment:           state.targetEnv,
			OpID:                  msg.OpID,
			OpKind:                OpRollback,
			DeliveryStage:         rollbackDeliveryStage(state.targetEnv),
			FromEnv:               state.targetEnv,
			ToEnv:                 state.targetEnv,
			Image:                 image,
			RenderedPath:          filepath.ToSlash(filepath.Join(state.rollbackDir, "rendered.yaml")),
			ConfigPath:            filepath.ToSlash(filepath.Join(state.rollbackDir, "deployment.yaml")),
			RollbackSafe:          rollbackSafeDefaultPtr(),
			RollbackSourceRelease: state.sourceRelease.ID,
			RollbackScope:         state.scope,
			CreatedAt:             time.Now().UTC(),
		},
	); err != nil {
		return promotionStageOutcome{
			message:   "",
			artifacts: state.outcome.artifacts,
		}, err
	}

	state.outcome.message = fmt.Sprintf(
		"rolled back %s to release %s using %s scope",
		state.targetEnv,
		shortID(state.sourceRelease.ID),
		state.scope,
	)
	return promotionStageOutcome(state.outcome), nil
}

func renderRollbackFromCurrentSpecArtifacts(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	state *rollbackExecutionState,
) (transitionArtifactSets, error) {
	sets := newTransitionArtifactSets()
	imageByEnv, err := loadManifestImageTags(artifacts, msg.ProjectID, state.spec)
	if err != nil {
		return sets, err
	}
	imageByEnv[state.targetEnv] = state.sourceImage
	sets.kustomizeArtifacts, err = writeKustomizeRepoFiles(artifacts, msg.ProjectID, state.spec, imageByEnv)
	if err != nil {
		return sets, err
	}
	overlayArtifacts, err := forceOverlayImageForEnvironment(
		artifacts,
		msg.ProjectID,
		state.targetEnv,
		state.sourceImage,
	)
	sets.kustomizeArtifacts = append(sets.kustomizeArtifacts, overlayArtifacts...)
	if err != nil {
		return sets, err
	}
	rendered, err := renderEnvironmentManifestsFromRepo(artifacts, msg.ProjectID, state.targetEnv)
	if err != nil {
		return sets, err
	}
	sets.deployArtifacts, err = writeRenderedEnvArtifacts(
		artifacts,
		msg.ProjectID,
		filepath.ToSlash(filepath.Join("deploy", state.targetEnv)),
		rendered,
	)
	if err != nil {
		return sets, err
	}
	sets.transitionArtifacts, err = writeRenderedEnvArtifacts(
		artifacts,
		msg.ProjectID,
		state.rollbackDir,
		rendered,
	)
	if err != nil {
		return sets, err
	}
	return sets, nil
}

func renderRollbackFullStateArtifacts(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	state *rollbackExecutionState,
) (transitionArtifactSets, error) {
	sets := newTransitionArtifactSets()
	imageByEnv, err := loadManifestImageTags(artifacts, msg.ProjectID, state.spec)
	if err != nil {
		return sets, err
	}
	imageByEnv[state.targetEnv] = state.sourceImage
	sets.kustomizeArtifacts, err = writeKustomizeRepoFiles(artifacts, msg.ProjectID, state.spec, imageByEnv)
	if err != nil {
		return sets, err
	}
	overlayArtifacts, err := forceOverlayImageForEnvironment(
		artifacts,
		msg.ProjectID,
		state.targetEnv,
		state.sourceImage,
	)
	sets.kustomizeArtifacts = append(sets.kustomizeArtifacts, overlayArtifacts...)
	if err != nil {
		return sets, err
	}
	sets.deployArtifacts, err = writeRenderedEnvArtifacts(
		artifacts,
		msg.ProjectID,
		filepath.ToSlash(filepath.Join("deploy", state.targetEnv)),
		state.rendered,
	)
	if err != nil {
		return sets, err
	}
	sets.transitionArtifacts, err = writeRenderedEnvArtifacts(
		artifacts,
		msg.ProjectID,
		state.rollbackDir,
		state.rendered,
	)
	if err != nil {
		return sets, err
	}
	return sets, nil
}

func resolveRollbackReleaseImage(
	artifacts ArtifactStore,
	projectID string,
	release ReleaseRecord,
) (string, error) {
	if strings.TrimSpace(release.Image) != "" {
		return strings.TrimSpace(release.Image), nil
	}
	configSnapshot, err := readRollbackReleaseConfigSnapshot(artifacts, projectID, release)
	if err != nil {
		return "", err
	}
	if len(configSnapshot) > 0 {
		if image := strings.TrimSpace(parseDeploymentImage(configSnapshot)); image != "" {
			return image, nil
		}
	}
	renderedSnapshot, err := readRollbackRenderedSnapshot(artifacts, projectID, release)
	if err != nil {
		return "", err
	}
	if image := strings.TrimSpace(parseDeploymentImage([]byte(renderedSnapshot.deployment))); image != "" {
		return image, nil
	}
	return "", nil
}

func readRollbackReleaseConfigSnapshot(
	artifacts ArtifactStore,
	projectID string,
	release ReleaseRecord,
) ([]byte, error) {
	paths := []string{}
	if path := strings.Trim(strings.TrimSpace(release.ConfigPath), "/"); path != "" {
		paths = append(paths, path)
	}
	if rendered := strings.Trim(strings.TrimSpace(release.RenderedPath), "/"); rendered != "" {
		if base, ok := strings.CutSuffix(rendered, "/rendered.yaml"); ok {
			paths = append(paths, base+"/deployment.yaml")
		}
	}
	for _, path := range paths {
		raw, err := artifacts.ReadFile(projectID, path)
		if err == nil {
			return raw, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to read rollback config snapshot %q: %w", path, err)
		}
	}
	return nil, nil
}

func readRollbackRenderedSnapshot(
	artifacts ArtifactStore,
	projectID string,
	release ReleaseRecord,
) (renderedProjectManifests, error) {
	renderedPath := strings.Trim(strings.TrimSpace(release.RenderedPath), "/")
	if renderedPath == "" {
		return renderedProjectManifests{}, errors.New("rollback rendered snapshot is missing")
	}
	raw, err := artifacts.ReadFile(projectID, renderedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return renderedProjectManifests{}, errors.New("rollback rendered snapshot is missing")
		}
		return renderedProjectManifests{}, fmt.Errorf("failed to read rollback rendered snapshot: %w", err)
	}
	deployment, service, splitErr := splitRenderedManifests(raw)
	if splitErr != nil {
		return renderedProjectManifests{}, splitErr
	}
	return renderedProjectManifests{
		deployment:    deployment,
		service:       service,
		kustomization: "",
		rendered:      string(raw),
	}, nil
}

func applyRollbackConfigToSpec(spec ProjectSpec, env string, vars map[string]string) ProjectSpec {
	spec = normalizeProjectSpec(spec)
	if spec.Environments == nil {
		spec.Environments = map[string]EnvConfig{}
	}
	cfg := spec.Environments[env]
	cfgVars := make(map[string]string, len(vars))
	maps.Copy(cfgVars, vars)
	cfg.Vars = cfgVars
	spec.Environments[env] = cfg
	return spec
}

func commitRollbackManifestsRepo(
	ctx context.Context,
	artifacts ArtifactStore,
	projectID string,
	targetEnv string,
	opID string,
	scope RollbackScope,
	releaseID string,
) error {
	manifestsDir := manifestsRepoDir(artifacts, projectID)
	if err := ensureLocalGitRepo(ctx, manifestsDir); err != nil {
		return err
	}
	_, err := gitCommitIfChanged(
		ctx,
		manifestsDir,
		fmt.Sprintf(
			"platform-sync: rollback %s to %s (%s %s)",
			targetEnv,
			shortID(releaseID),
			scope,
			shortID(opID),
		),
	)
	return err
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
			ConfigPath: filepath.ToSlash(
				filepath.Join(
					transition.artifactDir,
					fmt.Sprintf("%s-to-%s", fromEnv, toEnv),
					"deployment.yaml",
				),
			),
			RollbackSafe:          rollbackSafeDefaultPtr(),
			RollbackSourceRelease: "",
			RollbackScope:         "",
			CreatedAt:             time.Now().UTC(),
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
	case kind == OpRollback:
		stage = rollbackDeliveryStage(toEnv)
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
