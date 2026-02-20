package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
