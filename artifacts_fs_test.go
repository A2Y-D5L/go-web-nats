package platform_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	platform "github.com/a2y-d5l/go-web-nats"
)

func TestStore_FSArtifactsListFilesSkipsGitDirectories(t *testing.T) {
	root := t.TempDir()
	artifacts := platform.NewFSArtifacts(root)

	projectID := "p1"
	projectDir, err := artifacts.EnsureProjectDir(projectID)
	if err != nil {
		t.Fatalf("ensure project dir: %v", err)
	}
	_, writeErr := artifacts.WriteFile(projectID, "repos/source/main.go", []byte("package main\n"))
	if writeErr != nil {
		t.Fatalf("write file: %v", writeErr)
	}

	gitConfig := filepath.Join(projectDir, "repos", "source", ".git", "config")
	mkdirErr := os.MkdirAll(filepath.Dir(gitConfig), 0o755)
	if mkdirErr != nil {
		t.Fatalf("mkdir git dir: %v", mkdirErr)
	}
	writeGitConfigErr := os.WriteFile(gitConfig, []byte("[core]\n"), 0o644)
	if writeGitConfigErr != nil {
		t.Fatalf("write git config: %v", writeGitConfigErr)
	}

	files, err := artifacts.ListFiles(projectID)
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	for _, f := range files {
		if strings.Contains(f, "/.git/") || strings.HasPrefix(f, ".git/") {
			t.Fatalf("expected .git paths to be filtered, got %q", f)
		}
	}
	if len(files) != 1 || files[0] != "repos/source/main.go" {
		t.Fatalf("unexpected file list: %#v", files)
	}
}
