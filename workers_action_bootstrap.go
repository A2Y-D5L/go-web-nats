package platform

import (
	"context"
	"fmt"
	"time"
)

type repoBootstrapOutcome struct {
	message   string
	artifacts []string
}

func newRepoBootstrapOutcome() repoBootstrapOutcome {
	return repoBootstrapOutcome{
		message:   "",
		artifacts: nil,
	}
}

func repoBootstrapWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("repo bootstrap worker starting")
	_ = markOpStepStart(
		ctx,
		store,
		msg.OpID,
		"repoBootstrap",
		stepStart,
		"bootstrap source and manifests repos",
	)

	spec := normalizeProjectSpec(msg.Spec)
	outcome := newRepoBootstrapOutcome()
	var err error

	switch msg.Kind {
	case OpCreate, OpUpdate:
		outcome, err = runRepoBootstrapCreateOrUpdate(ctx, artifacts, msg, spec)
	case OpDelete:
		outcome, err = runRepoBootstrapDelete(artifacts, msg.ProjectID)
	case OpCI:
		outcome = repoBootstrapOutcome{
			message:   "repo bootstrap skipped for ci operation",
			artifacts: nil,
		}
	case OpDeploy, OpPromote, OpRelease:
		outcome = repoBootstrapOutcome{
			message:   "repo bootstrap skipped for deployment/promotion/release operation",
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
			"repoBootstrap",
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
		"repoBootstrap",
		time.Now().UTC(),
		res.Message,
		"",
		res.Artifacts,
	)
	return res, nil
}

func runRepoBootstrapDelete(
	artifacts ArtifactStore,
	projectID string,
) (repoBootstrapOutcome, error) {
	planPath, err := artifacts.WriteFile(
		projectID,
		"repos/teardown-plan.txt",
		[]byte("archive source repo\narchive manifests repo\nremove project workspace\n"),
	)
	if err != nil {
		return repoBootstrapOutcome{}, err
	}
	return repoBootstrapOutcome{
		message:   "repository teardown plan generated",
		artifacts: []string{planPath},
	}, nil
}

func runRepoBootstrapCreateOrUpdate(
	ctx context.Context,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
) (repoBootstrapOutcome, error) {
	projectDir, sourceDir, manifestsDir, err := ensureBootstrapRepos(ctx, artifacts, msg.ProjectID)
	if err != nil {
		return repoBootstrapOutcome{}, err
	}

	touched := make([]string, 0, touchedArtifactsCap)
	err = seedSourceRepo(msg, spec, projectDir, sourceDir, &touched)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: touched}, err
	}
	err = seedManifestsRepo(msg, spec, projectDir, manifestsDir, &touched)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: touched}, err
	}
	err = commitBootstrapSeeds(ctx, msg, sourceDir, manifestsDir)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: touched}, err
	}

	webhookURL, err := configureSourceWebhook(ctx, msg, projectDir, sourceDir, &touched)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: touched}, err
	}
	err = writeBootstrapSummary(ctx, msg, projectDir, sourceDir, manifestsDir, webhookURL, &touched)
	if err != nil {
		return repoBootstrapOutcome{message: "", artifacts: touched}, err
	}
	return repoBootstrapOutcome{
		message:   "bootstrapped local source/manifests git repos and installed source webhook",
		artifacts: uniqueSorted(touched),
	}, nil
}
