package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

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

func commitWatcherEnabled() bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv("PAAS_ENABLE_COMMIT_WATCHER")))
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return enabled
}

func startSourceCommitWatcher(ctx context.Context, api *API) bool {
	if !commitWatcherEnabled() {
		return false
	}
	watcherLog := appLoggerForProcess().Source("sourceWatcher")
	go runSourceCommitWatcher(ctx, api, watcherLog)
	return true
}

func runSourceCommitWatcher(ctx context.Context, api *API, watcherLog sourceLogger) {
	ticker := time.NewTicker(commitWatcherPollInterval)
	defer ticker.Stop()
	lastSeenCommit := map[string]string{}
	for {
		scanSourceRepos(ctx, api, watcherLog, lastSeenCommit)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func scanSourceRepos(
	ctx context.Context,
	api *API,
	watcherLog sourceLogger,
	lastSeenCommit map[string]string,
) {
	projects, err := api.store.ListProjects(ctx)
	if err != nil {
		watcherLog.Warnf("list projects: %v", err)
		return
	}
	for _, project := range projects {
		sourceDir := sourceRepoDir(api.artifacts, project.ID)
		branch, commit, message, repoErr := gitHeadDetails(ctx, sourceDir)
		if repoErr != nil {
			if errors.Is(repoErr, os.ErrNotExist) {
				continue
			}
			watcherLog.Debugf("project=%s read source repo: %v", project.ID, repoErr)
			continue
		}
		if normalizeBranchValue(branch) != branchMain {
			continue
		}
		if shouldSkipSourceCommitMessage(message) {
			continue
		}
		commit = strings.TrimSpace(commit)
		if commit == "" {
			continue
		}
		if lastSeenCommit[project.ID] == commit {
			continue
		}
		lastSeenCommit[project.ID] = commit
		evt := SourceRepoWebhookEvent{
			ProjectID: project.ID,
			Repo:      "source",
			Branch:    branchMain,
			Ref:       "refs/heads/" + branchMain,
			Commit:    commit,
		}
		result, triggerErr := api.triggerSourceRepoCI(ctx, evt, "source.main.watcher")
		if triggerErr != nil {
			watcherLog.Warnf(
				"project=%s commit=%s trigger failed: %v",
				project.ID,
				shortID(commit),
				triggerErr,
			)
			continue
		}
		if !result.accepted {
			watcherLog.Debugf(
				"project=%s commit=%s skipped: %s",
				project.ID,
				shortID(commit),
				result.reason,
			)
			continue
		}
		watcherLog.Infof(
			"project=%s commit=%s triggered op=%s",
			project.ID,
			shortID(commit),
			result.op.ID,
		)
	}
}

func sourceRepoDir(artifacts ArtifactStore, projectID string) string {
	return filepath.Join(artifacts.ProjectDir(projectID), "repos", "source")
}

func manifestsRepoDir(artifacts ArtifactStore, projectID string) string {
	return filepath.Join(artifacts.ProjectDir(projectID), "repos", "manifests")
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
  %s*)
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
`, branchMain, platformSyncPrefix, projectRelPathPartsMin, endpoint, projectID)
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
