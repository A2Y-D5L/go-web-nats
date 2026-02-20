package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

////////////////////////////////////////////////////////////////////////////////
// Worker actions: real-world-ish PaaS artifacts
////////////////////////////////////////////////////////////////////////////////

func registrationWorkerAction(
	ctx context.Context,
	store *Store,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
) (WorkerResultMsg, error) {
	stepStart := time.Now().UTC()
	res := newWorkerResultMsg("registration worker starting")
	_ = markOpStepStart(ctx, store, msg.OpID, "registrar", stepStart, "register app configuration")

	spec := normalizeProjectSpec(msg.Spec)
	outcome := newRepoBootstrapOutcome()
	var err error

	switch msg.Kind {
	case OpCreate, OpUpdate:
		outcome, err = runRegistrationCreateOrUpdate(artifacts, msg, spec)
	case OpDelete:
		outcome, err = runRegistrationDelete(artifacts, msg.ProjectID, msg.OpID)
	case OpCI:
		outcome = repoBootstrapOutcome{
			message:   "registration skipped for ci operation",
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
			"registrar",
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
		"registrar",
		time.Now().UTC(),
		res.Message,
		"",
		res.Artifacts,
	)
	return res, nil
}

func runRegistrationCreateOrUpdate(
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
) (repoBootstrapOutcome, error) {
	if err := validateProjectSpec(spec); err != nil {
		return newRepoBootstrapOutcome(), err
	}
	_, _ = artifacts.EnsureProjectDir(msg.ProjectID)
	projectYAMLPath, err := artifacts.WriteFile(
		msg.ProjectID,
		"registration/project.yaml",
		renderProjectConfigYAML(spec),
	)
	if err != nil {
		return newRepoBootstrapOutcome(), err
	}
	registrationPath, err := artifacts.WriteFile(
		msg.ProjectID,
		"registration/registration.json",
		mustJSON(map[string]any{
			"project_id": msg.ProjectID,
			"op_id":      msg.OpID,
			"kind":       msg.Kind,
			"registered": time.Now().UTC(),
			"name":       spec.Name,
			"runtime":    spec.Runtime,
		}),
	)
	if err != nil {
		return repoBootstrapOutcome{
			message:   "",
			artifacts: []string{projectYAMLPath},
		}, err
	}
	return repoBootstrapOutcome{
		message:   "project registration upserted",
		artifacts: []string{projectYAMLPath, registrationPath},
	}, nil
}

func runRegistrationDelete(
	artifacts ArtifactStore,
	projectID, opID string,
) (repoBootstrapOutcome, error) {
	deregisterBody := fmt.Appendf(
		nil,
		"deregister requested at %s\nop=%s\n",
		time.Now().UTC().Format(time.RFC3339),
		opID,
	)
	deregisterPath, err := artifacts.WriteFile(
		projectID,
		"registration/deregister.txt",
		deregisterBody,
	)
	if err != nil {
		return newRepoBootstrapOutcome(), err
	}
	return repoBootstrapOutcome{
		message:   "project deregistration staged",
		artifacts: []string{deregisterPath},
	}, nil
}

func localAPIBaseURL() string {
	base := strings.TrimSpace(os.Getenv("PAAS_LOCAL_API_BASE_URL"))
	if base == "" {
		base = "http://" + httpAddr
	}
	return strings.TrimRight(base, "/")
}

func sourceWebhookEndpoint() string {
	return localAPIBaseURL() + "/api/webhooks/source"
}

func sourceRepoDir(artifacts ArtifactStore, projectID string) string {
	return filepath.Join(artifacts.ProjectDir(projectID), "repos", "source")
}

func manifestsRepoDir(artifacts ArtifactStore, projectID string) string {
	return filepath.Join(artifacts.ProjectDir(projectID), "repos", "manifests")
}

func runGitCmd(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func gitHasStagedChanges(ctx context.Context, dir string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet", "--exit-code")
	cmd.Dir = dir
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("git diff --cached --quiet: %w", err)
}

func gitCommitIfChanged(ctx context.Context, dir, message string) (bool, error) {
	runCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	if err := runGitCmd(runCtx, dir, "add", "-A"); err != nil {
		return false, err
	}
	changed, err := gitHasStagedChanges(runCtx, dir)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	commitErr := runGitCmd(runCtx, dir, "commit", "-m", message)
	if commitErr != nil {
		return false, commitErr
	}
	return true, nil
}

func gitRevParse(ctx context.Context, dir, ref string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", "rev-parse", ref)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("git rev-parse %s: %w: %s", ref, err, msg)
		}
		return "", fmt.Errorf("git rev-parse %s: %w", ref, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func ensureLocalGitRepo(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, dirModePrivateRead); err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		initErr := runGitCmd(runCtx, dir, "init", "-b", branchMain)
		if initErr != nil {
			// Fallback for older git versions that do not support `-b`.
			fallbackErr := runGitCmd(runCtx, dir, "init")
			if fallbackErr != nil {
				return fmt.Errorf("git init failed: %w; fallback failed: %w", initErr, fallbackErr)
			}
		}
	}
	if err := runGitCmd(runCtx, dir, "checkout", "-B", branchMain); err != nil {
		return err
	}
	if err := runGitCmd(runCtx, dir, "config", "user.name", "Local PaaS Bot"); err != nil {
		return err
	}
	if err := runGitCmd(runCtx, dir, "config", "user.email", "paas-local@example.invalid"); err != nil {
		return err
	}
	if err := runGitCmd(runCtx, dir, "config", "commit.gpgsign", "false"); err != nil {
		return err
	}
	return nil
}

func writeFileIfMissing(path string, data []byte) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), dirModePrivateRead); err != nil {
		return false, err
	}
	writeErr := os.WriteFile(path, data, fileModePrivate)
	if writeErr != nil {
		return false, writeErr
	}
	return true, nil
}

func upsertFile(path string, data []byte) (bool, error) {
	prev, err := os.ReadFile(path)
	if err == nil && string(prev) == string(data) {
		return false, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	mkdirErr := os.MkdirAll(filepath.Dir(path), dirModePrivateRead)
	if mkdirErr != nil {
		return false, mkdirErr
	}
	writeErr := os.WriteFile(path, data, fileModePrivate)
	if writeErr != nil {
		return false, writeErr
	}
	return true, nil
}

func relPath(baseDir, fullPath string) string {
	rel, err := filepath.Rel(baseDir, fullPath)
	if err != nil {
		return filepath.ToSlash(fullPath)
	}
	return filepath.ToSlash(rel)
}

func renderSourceWebhookHookScript(projectID, endpoint string) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu

if ! command -v git >/dev/null 2>&1; then
  exit 0
fi
if ! command -v curl >/dev/null 2>&1; then
  exit 0
fi

branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
if [ "$branch" != %q ]; then
  exit 0
fi

msg="$(git log -1 --pretty=%%s 2>/dev/null || true)"
case "$msg" in
  platform-sync:*)
    exit 0
    ;;
esac

commit="$(git rev-parse HEAD 2>/dev/null || true)"
if [ -z "$commit" ]; then
  exit 0
fi

curl -fsS --max-time %d \
  -H 'Content-Type: application/json' \
  -X POST '%s' \
  -d "{\"project_id\":\"%s\",\"repo\":\"source\",\"branch\":\"${branch}\",\"ref\":\"refs/heads/${branch}\",\"commit\":\"${commit}\"}" \
  >/dev/null || true
`, branchMain, projectRelPathPartsMin, endpoint, projectID)
}

func installSourceWebhookHooks(repoDir, projectID, endpoint string) error {
	script := []byte(renderSourceWebhookHookScript(projectID, endpoint))
	for _, hook := range []string{"post-commit", "post-merge"} {
		hookPath := filepath.Join(repoDir, ".git", "hooks", hook)
		if err := os.MkdirAll(filepath.Dir(hookPath), dirModePrivateRead); err != nil {
			return err
		}
		if err := os.WriteFile(hookPath, script, fileModeExecPrivate); err != nil {
			return err
		}
		if err := os.Chmod(hookPath, fileModeExecPrivate); err != nil {
			return err
		}
	}
	return nil
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			continue
		}
		set[filepath.ToSlash(v)] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}

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

func ensureBootstrapRepos(
	ctx context.Context,
	artifacts ArtifactStore,
	projectID string,
) (string, string, string, error) {
	projectDir, err := artifacts.EnsureProjectDir(projectID)
	if err != nil {
		return "", "", "", err
	}
	sourceDir := sourceRepoDir(artifacts, projectID)
	manifestsDir := manifestsRepoDir(artifacts, projectID)
	sourceRepoErr := ensureLocalGitRepo(ctx, sourceDir)
	if sourceRepoErr != nil {
		return "", "", "", sourceRepoErr
	}
	manifestsRepoErr := ensureLocalGitRepo(ctx, manifestsDir)
	if manifestsRepoErr != nil {
		return "", "", "", manifestsRepoErr
	}
	return projectDir, sourceDir, manifestsDir, nil
}

func seedSourceRepo(
	msg ProjectOpMsg,
	spec ProjectSpec,
	projectDir, sourceDir string,
	touched *[]string,
) error {
	sourceReadme := filepath.Join(sourceDir, "README.md")
	sourceReadmeBody := fmt.Appendf(nil, "# %s source\n\nRuntime: %s\n", spec.Name, spec.Runtime)
	readmeCreated, err := writeFileIfMissing(
		sourceReadme,
		sourceReadmeBody,
	)
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, sourceReadme, readmeCreated)

	sourceMain := filepath.Join(sourceDir, "main.go")
	sourceMainBody := fmt.Appendf(nil, `package main

import "fmt"

func main() { fmt.Println("hello from %s") }
`, spec.Name)
	mainCreated, err := writeFileIfMissing(sourceMain, sourceMainBody)
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, sourceMain, mainCreated)

	sourceRepoMeta := filepath.Join(sourceDir, ".paas", "repo.json")
	metaUpdated, err := upsertFile(sourceRepoMeta, mustJSON(map[string]any{
		"project_id": msg.ProjectID,
		"repo":       "source",
		"path":       sourceDir,
		"branch":     branchMain,
	}))
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, sourceRepoMeta, metaUpdated)
	return nil
}

func seedManifestsRepo(
	msg ProjectOpMsg,
	spec ProjectSpec,
	projectDir, manifestsDir string,
	touched *[]string,
) error {
	manifestsReadme := filepath.Join(manifestsDir, "README.md")
	manifestsReadmeBody := fmt.Appendf(
		nil,
		"# %s manifests\n\nTarget image: local/%s:latest\n",
		spec.Name,
		safeName(spec.Name),
	)
	readmeCreated, err := writeFileIfMissing(
		manifestsReadme,
		manifestsReadmeBody,
	)
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, manifestsReadme, readmeCreated)

	manifestsRepoMeta := filepath.Join(manifestsDir, ".paas", "repo.json")
	metaUpdated, err := upsertFile(manifestsRepoMeta, mustJSON(map[string]any{
		"project_id": msg.ProjectID,
		"repo":       "manifests",
		"path":       manifestsDir,
		"branch":     branchMain,
	}))
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, manifestsRepoMeta, metaUpdated)
	return nil
}

func commitBootstrapSeeds(
	ctx context.Context,
	msg ProjectOpMsg,
	sourceDir, manifestsDir string,
) error {
	_, sourceCommitErr := gitCommitIfChanged(
		ctx,
		sourceDir,
		fmt.Sprintf("platform-sync: bootstrap source repo (%s)", shortID(msg.OpID)),
	)
	if sourceCommitErr != nil {
		return sourceCommitErr
	}
	_, manifestsCommitErr := gitCommitIfChanged(
		ctx,
		manifestsDir,
		fmt.Sprintf("platform-sync: bootstrap manifests repo (%s)", shortID(msg.OpID)),
	)
	return manifestsCommitErr
}

func configureSourceWebhook(
	ctx context.Context,
	msg ProjectOpMsg,
	projectDir, sourceDir string,
	touched *[]string,
) (string, error) {
	webhookURL := sourceWebhookEndpoint()
	if err := installSourceWebhookHooks(sourceDir, msg.ProjectID, webhookURL); err != nil {
		return "", err
	}
	webhookMeta := filepath.Join(sourceDir, ".paas", "webhook.json")
	updated, err := upsertFile(webhookMeta, mustJSON(map[string]any{
		"project_id": msg.ProjectID,
		"repo":       "source",
		"branch":     branchMain,
		"endpoint":   webhookURL,
		"hooks":      []string{"post-commit", "post-merge"},
	}))
	if err != nil {
		return "", err
	}
	recordTouched(projectDir, touched, webhookMeta, updated)
	_, commitErr := gitCommitIfChanged(
		ctx,
		sourceDir,
		fmt.Sprintf("platform-sync: configure source webhook (%s)", shortID(msg.OpID)),
	)
	if commitErr != nil {
		return "", commitErr
	}
	return webhookURL, nil
}

func writeBootstrapSummary(
	ctx context.Context,
	msg ProjectOpMsg,
	projectDir, sourceDir, manifestsDir, webhookURL string,
	touched *[]string,
) error {
	sourceHead, _ := gitRevParse(ctx, sourceDir, "HEAD")
	manifestsHead, _ := gitRevParse(ctx, manifestsDir, "HEAD")
	bootstrapInfo := filepath.Join(projectDir, "repos", "bootstrap-local.json")
	updated, err := upsertFile(bootstrapInfo, mustJSON(map[string]any{
		"project_id":         msg.ProjectID,
		"source_repo_path":   sourceDir,
		"source_branch":      branchMain,
		"source_head":        sourceHead,
		"manifests_repo":     manifestsDir,
		"manifests_branch":   branchMain,
		"manifests_head":     manifestsHead,
		"webhook_endpoint":   webhookURL,
		"webhook_event_repo": "source",
	}))
	if err != nil {
		return err
	}
	recordTouched(projectDir, touched, bootstrapInfo, updated)
	return nil
}

func recordTouched(projectDir string, touched *[]string, fullPath string, changed bool) {
	if !changed {
		return
	}
	*touched = append(*touched, relPath(projectDir, fullPath))
}

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

func shortID(id string) string {
	if len(id) <= shortIDLength {
		return id
	}
	return id[:shortIDLength]
}

func sortedKeys[K ~string, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func yamlQuoted(v string) string {
	return fmt.Sprintf("%q", v)
}

func renderProjectConfigYAML(spec ProjectSpec) []byte {
	spec = normalizeProjectSpec(spec)
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: %s\n", spec.APIVersion)
	fmt.Fprintf(&b, "kind: %s\n", spec.Kind)
	fmt.Fprintf(&b, "name: %s\n", spec.Name)
	fmt.Fprintf(&b, "runtime: %s\n", spec.Runtime)
	if len(spec.Capabilities) > 0 {
		b.WriteString("capabilities:\n")
		for _, c := range spec.Capabilities {
			fmt.Fprintf(&b, "  - %s\n", c)
		}
	}
	b.WriteString("environments:\n")
	for _, env := range sortedKeys(spec.Environments) {
		cfg := spec.Environments[env]
		fmt.Fprintf(&b, "  %s:\n", env)
		b.WriteString("    vars:\n")
		keys := sortedKeys(cfg.Vars)
		if len(keys) == 0 {
			b.WriteString("      {}\n")
		}
		for _, k := range keys {
			fmt.Fprintf(&b, "      %s: %s\n", k, yamlQuoted(cfg.Vars[k]))
		}
	}
	b.WriteString("networkPolicies:\n")
	fmt.Fprintf(&b, "  ingress: %s\n", spec.NetworkPolicies.Ingress)
	fmt.Fprintf(&b, "  egress: %s\n", spec.NetworkPolicies.Egress)
	return []byte(b.String())
}

func preferredEnvironment(spec ProjectSpec) (string, map[string]string) {
	spec = normalizeProjectSpec(spec)
	if env, ok := spec.Environments["dev"]; ok {
		return "dev", env.Vars
	}
	names := sortedKeys(spec.Environments)
	if len(names) == 0 {
		return "default", map[string]string{}
	}
	first := names[0]
	return first, spec.Environments[first].Vars
}

func renderDeploymentManifest(spec ProjectSpec, image string) string {
	spec = normalizeProjectSpec(spec)
	envName, vars := preferredEnvironment(spec)
	name := safeName(spec.Name)
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: apps/v1\n")
	fmt.Fprintf(&b, "kind: Deployment\n")
	fmt.Fprintf(&b, "metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", name)
	fmt.Fprintf(&b, "spec:\n")
	fmt.Fprintf(&b, "  replicas: 1\n")
	fmt.Fprintf(&b, "  selector:\n")
	fmt.Fprintf(&b, "    matchLabels:\n")
	fmt.Fprintf(&b, "      app: %s\n", name)
	fmt.Fprintf(&b, "  template:\n")
	fmt.Fprintf(&b, "    metadata:\n")
	fmt.Fprintf(&b, "      labels:\n")
	fmt.Fprintf(&b, "        app: %s\n", name)
	fmt.Fprintf(&b, "      annotations:\n")
	fmt.Fprintf(&b, "        platform.example.com/environment: %s\n", envName)
	fmt.Fprintf(&b, "        platform.example.com/ingress: %s\n", spec.NetworkPolicies.Ingress)
	fmt.Fprintf(&b, "        platform.example.com/egress: %s\n", spec.NetworkPolicies.Egress)
	fmt.Fprintf(&b, "    spec:\n")
	fmt.Fprintf(&b, "      containers:\n")
	fmt.Fprintf(&b, "      - name: app\n")
	fmt.Fprintf(&b, "        image: %s\n", image)
	fmt.Fprintf(&b, "        imagePullPolicy: IfNotPresent\n")
	fmt.Fprintf(&b, "        ports:\n")
	fmt.Fprintf(&b, "        - containerPort: 8080\n")
	keys := sortedKeys(vars)
	if len(keys) > 0 {
		fmt.Fprintf(&b, "        env:\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "        - name: %s\n", k)
			fmt.Fprintf(&b, "          value: %s\n", yamlQuoted(vars[k]))
		}
	}
	return b.String()
}

func renderServiceManifest(spec ProjectSpec) string {
	spec = normalizeProjectSpec(spec)
	name := safeName(spec.Name)
	return fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
spec:
  selector:
    app: %s
  ports:
  - name: http
    port: 80
    targetPort: 8080
`, name, name)
}

func safeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "project"
	}
	var out []rune
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r)
		case r >= '0' && r <= '9':
			out = append(out, r)
		case r == '-' || r == '_':
			out = append(out, '-')
		case r == ' ':
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "project"
	}
	return string(out)
}
