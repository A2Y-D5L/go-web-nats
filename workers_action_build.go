package platform

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"
)

const (
	imageBuildDockerfilePath = "build/Dockerfile"
	imageBuildPublishPath    = "build/publish-local-daemon.json"
	imageBuildTagPath        = "build/image.txt"
	buildKitSummaryPath      = "build/buildkit-summary.txt"
	buildKitMetadataPath     = "build/buildkit-metadata.json"
	buildKitLogPath          = "build/buildkit.log"
	buildKitArtifactsCount   = 3
)

func imageBuilderWorkerActionWithMode(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	modeResolution imageBuilderModeResolution,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("image builder worker starting")
	_ = markOpStepStart(
		ctx,
		store,
		msg.OpID,
		"imageBuilder",
		stepStart,
		imageBuilderStepStartMessage(modeResolution),
	)

	spec := normalizeProjectSpec(msg.Spec)
	imageTag := fmt.Sprintf("local/%s:%s", safeName(spec.Name), shortID(msg.OpID))
	outcome := newRepoBootstrapOutcome()
	var err error

	switch msg.Kind {
	case OpCreate, OpUpdate, OpCI:
		outcome, err = runImageBuilderBuildWithMode(ctx, artifacts, msg, spec, imageTag, modeResolution)
	case OpDelete:
		outcome, err = runImageBuilderDelete(artifacts, msg.ProjectID, msg.OpID)
	case OpDeploy, OpPromote, OpRelease:
		outcome = repoBootstrapOutcome{
			message:   "image build skipped for deployment/promotion/release operation",
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
			"imageBuilder",
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
		"imageBuilder",
		time.Now().UTC(),
		res.Message,
		"",
		res.Artifacts,
	)
	return res, nil
}

func runImageBuilderBuild(
	ctx context.Context,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	imageTag string,
) (repoBootstrapOutcome, error) {
	return runImageBuilderBuildWithMode(
		ctx,
		artifacts,
		msg,
		spec,
		imageTag,
		resolveEffectiveImageBuilderMode(ctx),
	)
}

func runImageBuilderBuildWithMode(
	ctx context.Context,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	imageTag string,
	modeResolution imageBuilderModeResolution,
) (repoBootstrapOutcome, error) {
	dockerfileBody := renderImageBuilderDockerfile(spec)
	dockerfilePath, err := artifacts.WriteFile(msg.ProjectID, imageBuildDockerfilePath, dockerfileBody)
	if err != nil {
		return newRepoBootstrapOutcome(), err
	}

	if modeResolution.policyError != "" {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: []string{dockerfilePath},
		}, errors.New(modeResolution.policyError)
	}

	mode := modeResolution.effectiveMode
	var backend imageBuilderBackend = artifactImageBuilderBackend{}
	if mode == imageBuilderModeBuildKit {
		backend = buildKitImageBuilderBackend{}
	}
	req := imageBuildRequest{
		OpID:              msg.OpID,
		ProjectID:         msg.ProjectID,
		Spec:              spec,
		ImageTag:          imageTag,
		ContextDir:        sourceRepoDir(artifacts, msg.ProjectID),
		DockerfileBody:    dockerfileBody,
		DockerfileRelPath: imageBuildDockerfilePath,
	}

	outcome, err := runImageBuilderBuildWithBackend(
		ctx,
		artifacts,
		msg,
		modeResolution,
		backend,
		req,
		[]string{dockerfilePath},
	)
	if err != nil {
		return outcome, err
	}
	return outcome, nil
}

func selectImageBuilderBackendName(mode imageBuilderMode) string {
	if mode == imageBuilderModeBuildKit {
		return string(imageBuilderModeBuildKit)
	}
	return string(imageBuilderModeArtifact)
}

func runImageBuilderBuildWithBackend(
	ctx context.Context,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	modeResolution imageBuilderModeResolution,
	backend imageBuilderBackend,
	req imageBuildRequest,
	presetArtifacts []string,
) (repoBootstrapOutcome, error) {
	outcome := repoBootstrapOutcome{
		message:   "",
		artifacts: append([]string{}, presetArtifacts...),
	}

	buildCtx, cancel := context.WithTimeout(ctx, buildOpTimeout)
	defer cancel()

	result, backendErr := backend.build(buildCtx, req)
	buildKitArtifacts, writeBuildKitErr := maybeWriteBuildKitArtifacts(
		artifacts,
		msg,
		modeResolution,
		backend,
		req,
		result,
		backendErr,
	)
	outcome.artifacts = append(outcome.artifacts, buildKitArtifacts...)
	if writeBuildKitErr != nil {
		if backendErr != nil {
			return outcome, errors.Join(backendErr, writeBuildKitErr)
		}
		return outcome, writeBuildKitErr
	}
	if backendErr != nil {
		return outcome, backendErr
	}

	publishPath, err := writeImagePublishArtifacts(
		artifacts,
		msg,
		req.Spec,
		req.ImageTag,
		modeResolution,
		backend,
	)
	if err != nil {
		return outcome, err
	}
	outcome.artifacts = append(outcome.artifacts, publishPath)
	imagePath, err := artifacts.WriteFile(msg.ProjectID, imageBuildTagPath, []byte(req.ImageTag+"\n"))
	if err != nil {
		return outcome, err
	}
	outcome.artifacts = append(outcome.artifacts, imagePath)
	outcome.artifacts = uniqueSorted(outcome.artifacts)

	message := strings.TrimSpace(result.message)
	if message == "" {
		message = "container image built and published to local daemon"
	}
	outcome.message = message
	return outcome, nil
}

func maybeWriteBuildKitArtifacts(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	modeResolution imageBuilderModeResolution,
	backend imageBuilderBackend,
	req imageBuildRequest,
	result imageBuildResult,
	backendErr error,
) ([]string, error) {
	if modeResolution.effectiveMode != imageBuilderModeBuildKit {
		return nil, nil
	}
	metadata := map[string]any{
		"project_id":      msg.ProjectID,
		"op_id":           msg.OpID,
		"builder_backend": backend.name(),
		"image":           req.ImageTag,
		"runtime":         req.Spec.Runtime,
		"context_dir":     req.ContextDir,
		"dockerfile_path": req.DockerfileRelPath,
		"status":          "ok",
		"completed_at":    time.Now().UTC().Format(time.RFC3339),
	}
	appendBuilderModeFields(metadata, modeResolution)
	if len(result.metadata) > 0 {
		maps.Copy(metadata, result.metadata)
	}
	summary := strings.TrimSpace(result.summary)
	if summary == "" {
		summary = "buildkit build completed"
	}
	if backendErr != nil {
		metadata["status"] = "failed"
		metadata["failure"] = backendErr.Error()
		summary = fmt.Sprintf("buildkit build failed: %v", backendErr)
	}
	logBody := strings.TrimSpace(result.logs)
	if logBody == "" {
		logBody = "(no buildkit log output)"
	}

	written := make([]string, 0, buildKitArtifactsCount)
	summaryPath, err := artifacts.WriteFile(msg.ProjectID, buildKitSummaryPath, []byte(summary+"\n"))
	if err != nil {
		return written, err
	}
	written = append(written, summaryPath)
	metadataPath, err := artifacts.WriteFile(msg.ProjectID, buildKitMetadataPath, mustJSON(metadata))
	if err != nil {
		return written, err
	}
	written = append(written, metadataPath)
	logPath, err := artifacts.WriteFile(msg.ProjectID, buildKitLogPath, []byte(logBody+"\n"))
	if err != nil {
		return written, err
	}
	written = append(written, logPath)
	return written, nil
}

func writeImagePublishArtifacts(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	imageTag string,
	modeResolution imageBuilderModeResolution,
	backend imageBuilderBackend,
) (string, error) {
	payload := map[string]any{
		"op_id":           msg.OpID,
		"project_id":      msg.ProjectID,
		"image":           imageTag,
		"runtime":         spec.Runtime,
		"published_at":    time.Now().UTC().Format(time.RFC3339),
		"daemon_target":   "local",
		"builder_backend": backend.name(),
	}
	appendBuilderModeFields(payload, modeResolution)
	return artifacts.WriteFile(msg.ProjectID, imageBuildPublishPath, mustJSON(payload))
}

func appendBuilderModeFields(payload map[string]any, modeResolution imageBuilderModeResolution) {
	payload["builder_mode"] = modeResolution.effectiveMode
	payload["effective_builder_mode"] = modeResolution.effectiveMode
	payload["requested_builder_mode"] = modeResolution.requestedMode
	payload["builder_mode_explicit"] = modeResolution.requestedExplicit
	if modeResolution.requestedWarning != "" {
		payload["builder_mode_warning"] = modeResolution.requestedWarning
	}
	if modeResolution.fallbackReason != "" {
		payload["builder_mode_fallback_reason"] = modeResolution.fallbackReason
	}
	if modeResolution.policyError != "" {
		payload["builder_mode_policy_error"] = modeResolution.policyError
	}
}

func imageBuilderStepStartMessage(modeResolution imageBuilderModeResolution) string {
	context := []string{
		fmt.Sprintf("requested=%s", modeResolution.requestedMode),
		fmt.Sprintf("effective=%s", modeResolution.effectiveMode),
	}
	if modeResolution.requestedExplicit {
		context = append(context, "explicit=true")
	}
	if modeResolution.fallbackReason != "" {
		context = append(context, "fallback="+modeResolution.fallbackReason)
	}
	if modeResolution.requestedWarning != "" {
		context = append(context, "warning="+modeResolution.requestedWarning)
	}
	if modeResolution.policyError != "" {
		context = append(context, "policy="+modeResolution.policyError)
	}
	return "build and publish image to local daemon (" + strings.Join(context, "; ") + ")"
}

type artifactImageBuilderBackend struct{}

func (artifactImageBuilderBackend) name() string {
	return string(imageBuilderModeArtifact)
}

func (artifactImageBuilderBackend) build(
	ctx context.Context,
	req imageBuildRequest,
) (imageBuildResult, error) {
	if err := ensureContextAlive(ctx); err != nil {
		return imageBuildResult{}, err
	}
	return imageBuildResult{
		message: "container image built and published to local daemon",
		summary: "artifact builder mode selected: generated local publish metadata only",
		metadata: map[string]any{
			"strategy":       "artifact",
			"context_dir":    req.ContextDir,
			"dockerfile":     req.DockerfileRelPath,
			"build_executed": false,
		},
		logs: "artifact mode performs no container build; outputs are local metadata artifacts",
	}, nil
}

func renderImageBuilderDockerfile(spec ProjectSpec) []byte {
	return fmt.Appendf(nil, `FROM alpine:3.20
WORKDIR /app
COPY . .
CMD ["sh", "-c", "echo running %s (%s) && sleep infinity"]
`, spec.Name, spec.Runtime)
}

func runImageBuilderDelete(
	artifacts ArtifactStore,
	projectID, opID string,
) (repoBootstrapOutcome, error) {
	pruneBody := fmt.Appendf(nil, "prune local image for project=%s op=%s\n", projectID, opID)
	prunePath, err := artifacts.WriteFile(projectID, "build/image-prune.txt", pruneBody)
	if err != nil {
		return newRepoBootstrapOutcome(), err
	}
	return repoBootstrapOutcome{
		message:   "container prune plan generated",
		artifacts: []string{prunePath},
	}, nil
}
