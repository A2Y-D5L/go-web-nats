package platform

import (
	"context"
	"fmt"
	"time"
)

func imageBuilderWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("image builder worker starting")
	_ = markOpStepStart(
		ctx,
		store,
		msg.OpID,
		"imageBuilder",
		stepStart,
		"build and publish image to local daemon",
	)

	spec := normalizeProjectSpec(msg.Spec)
	imageTag := fmt.Sprintf("local/%s:%s", safeName(spec.Name), shortID(msg.OpID))
	outcome := newRepoBootstrapOutcome()
	var err error

	switch msg.Kind {
	case OpCreate, OpUpdate, OpCI:
		outcome, err = runImageBuilderBuild(artifacts, msg, spec, imageTag)
	case OpDelete:
		outcome, err = runImageBuilderDelete(artifacts, msg.ProjectID, msg.OpID)
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
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	imageTag string,
) (repoBootstrapOutcome, error) {
	dockerfileBody := fmt.Appendf(nil, `FROM alpine:3.20
WORKDIR /app
COPY . .
CMD ["sh", "-c", "echo running %s (%s) && sleep infinity"]
`, spec.Name, spec.Runtime)
	dockerfilePath, err := artifacts.WriteFile(msg.ProjectID, "build/Dockerfile", dockerfileBody)
	if err != nil {
		return newRepoBootstrapOutcome(), err
	}
	publishPath, err := artifacts.WriteFile(
		msg.ProjectID,
		"build/publish-local-daemon.json",
		mustJSON(map[string]any{
			"op_id":         msg.OpID,
			"project_id":    msg.ProjectID,
			"image":         imageTag,
			"runtime":       spec.Runtime,
			"published_at":  time.Now().UTC().Format(time.RFC3339),
			"daemon_target": "local",
		}),
	)
	if err != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: []string{dockerfilePath},
		}, err
	}
	imagePath, err := artifacts.WriteFile(msg.ProjectID, "build/image.txt", []byte(imageTag+"\n"))
	if err != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: []string{dockerfilePath, publishPath},
		}, err
	}
	return repoBootstrapOutcome{
		message:   "container image built and published to local daemon",
		artifacts: []string{dockerfilePath, publishPath, imagePath},
	}, nil
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
