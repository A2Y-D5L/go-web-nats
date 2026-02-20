package platform_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	platform "github.com/a2y-d5l/go-web-nats"
)

func TestWorkers_RenderSourceWebhookHookScript(t *testing.T) {
	endpoint := "http://127.0.0.1:8080/api/webhooks/source"
	script := platform.RenderSourceWebhookHookScriptForTest("project-123", endpoint)

	if !strings.Contains(script, endpoint) {
		t.Fatalf("hook script missing endpoint: %s", script)
	}
	if !strings.Contains(script, `\"project_id\":\"project-123\"`) {
		t.Fatalf("hook script missing project id payload: %s", script)
	}
	if !strings.Contains(script, "platform-sync:*") {
		t.Fatalf("hook script missing platform-sync skip guard: %s", script)
	}
	if !strings.Contains(script, "command -v curl") {
		t.Fatalf("hook script missing curl dependency check: %s", script)
	}
}

func TestWorkers_EnsureLocalGitRepoAndCommit(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "source")
	if err := platform.EnsureLocalGitRepoForTest(context.Background(), repo); err != nil {
		t.Fatalf("ensure local git repo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatalf("missing .git dir: %v", err)
	}

	changed, err := platform.UpsertFileForTest(filepath.Join(repo, "README.md"), []byte("# test\n"))
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	if !changed {
		t.Fatal("expected file to be created")
	}

	committed, err := platform.GitCommitIfChangedForTest(
		context.Background(),
		repo,
		"platform-sync: seed test repo",
	)
	if err != nil {
		t.Fatalf("git commit if changed: %v", err)
	}
	if !committed {
		t.Fatal("expected commit to be created")
	}

	head, err := platform.GitRevParseForTest(context.Background(), repo, "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	if len(strings.TrimSpace(head)) < 8 {
		t.Fatalf("unexpected HEAD hash: %q", head)
	}
}
