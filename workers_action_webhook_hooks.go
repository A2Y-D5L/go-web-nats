package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
