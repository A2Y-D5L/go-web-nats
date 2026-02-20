package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func gitCommitSignature() object.Signature {
	return object.Signature{
		Name:  "Local PaaS Bot",
		Email: "paas-local@example.invalid",
		When:  time.Now().UTC(),
	}
}

func ensureContextAlive(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func openLocalRepo(dir string) (*gogit.Repository, error) {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("open repo: %w", err)
	}
	return repo, nil
}

func ensureRepoIdentity(repo *gogit.Repository) error {
	cfg, err := repo.Config()
	if err != nil {
		return fmt.Errorf("read repo config: %w", err)
	}
	cfg.User.Name = "Local PaaS Bot"
	cfg.User.Email = "paas-local@example.invalid"
	setErr := repo.Storer.SetConfig(cfg)
	if setErr != nil {
		return fmt.Errorf("write repo config: %w", setErr)
	}
	return nil
}

func checkoutMainBranch(repo *gogit.Repository) error {
	hasCommits, err := repoHasCommits(repo)
	if err != nil {
		return err
	}
	if !hasCommits {
		return repo.Storer.SetReference(
			plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName(branchMain)),
		)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("worktree: %w", err)
	}
	branchRef := plumbing.NewBranchReferenceName(branchMain)
	createErr := wt.Checkout(&gogit.CheckoutOptions{
		Hash:                      plumbing.Hash{},
		Branch:                    branchRef,
		Create:                    true,
		Force:                     true,
		Keep:                      false,
		SparseCheckoutDirectories: nil,
	})
	if createErr == nil {
		return nil
	}
	checkoutErr := wt.Checkout(&gogit.CheckoutOptions{
		Hash:                      plumbing.Hash{},
		Branch:                    branchRef,
		Create:                    false,
		Force:                     true,
		Keep:                      false,
		SparseCheckoutDirectories: nil,
	})
	if checkoutErr == nil {
		return nil
	}
	return fmt.Errorf("checkout %s: %w; fallback failed: %w", branchMain, createErr, checkoutErr)
}

func repoHasCommits(repo *gogit.Repository) (bool, error) {
	_, err := repo.Head()
	if err == nil {
		return true, nil
	}
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return false, nil
	}
	return false, fmt.Errorf("read head: %w", err)
}

func gitCommitIfChanged(ctx context.Context, dir, message string) (bool, error) {
	runCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	if err := ensureContextAlive(runCtx); err != nil {
		return false, err
	}
	repo, err := openLocalRepo(dir)
	if err != nil {
		return false, err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return false, fmt.Errorf("worktree: %w", err)
	}
	addErr := wt.AddGlob(".")
	if addErr != nil {
		return false, fmt.Errorf("stage changes: %w", addErr)
	}
	status, err := wt.Status()
	if err != nil {
		return false, fmt.Errorf("worktree status: %w", err)
	}
	if status.IsClean() {
		return false, nil
	}
	signature := gitCommitSignature()
	_, err = wt.Commit(message, &gogit.CommitOptions{
		All:               false,
		AllowEmptyCommits: false,
		Author:            &signature,
		Committer:         &signature,
		Parents:           nil,
		SignKey:           nil,
		Signer:            nil,
		Amend:             false,
	})
	if err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	ctxErr := ensureContextAlive(runCtx)
	if ctxErr != nil {
		return false, ctxErr
	}
	return true, nil
}

func gitRevParse(ctx context.Context, dir, ref string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	if err := ensureContextAlive(runCtx); err != nil {
		return "", err
	}
	repo, err := openLocalRepo(dir)
	if err != nil {
		return "", err
	}
	rev := plumbing.Revision(ref)
	hash, err := repo.ResolveRevision(rev)
	if err != nil {
		return "", fmt.Errorf("resolve revision %s: %w", ref, err)
	}
	if hash == nil {
		return "", fmt.Errorf("resolve revision %s: empty hash", ref)
	}
	return strings.TrimSpace(hash.String()), nil
}

func gitHeadDetails(ctx context.Context, dir string) (string, string, string, error) {
	runCtx, cancel := context.WithTimeout(ctx, gitReadTimeout)
	defer cancel()
	if err := ensureContextAlive(runCtx); err != nil {
		return "", "", "", err
	}
	repo, err := openLocalRepo(dir)
	if err != nil {
		return "", "", "", err
	}
	head, err := repo.Head()
	if err != nil {
		return "", "", "", fmt.Errorf("read head: %w", err)
	}
	branch := head.Name().Short()
	commitHash := head.Hash().String()
	commitObj, err := repo.CommitObject(head.Hash())
	if err != nil {
		return "", "", "", fmt.Errorf("read commit object: %w", err)
	}
	subject := strings.TrimSpace(commitObj.Message)
	if idx := strings.IndexByte(subject, '\n'); idx >= 0 {
		subject = strings.TrimSpace(subject[:idx])
	}
	return branch, commitHash, subject, nil
}

func ensureLocalGitRepo(ctx context.Context, dir string) error {
	if err := os.MkdirAll(dir, dirModePrivateRead); err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, gitOpTimeout)
	defer cancel()
	if err := ensureContextAlive(runCtx); err != nil {
		return err
	}
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		_, initErr := gogit.PlainInit(dir, false)
		if initErr != nil {
			return fmt.Errorf("initialize repo: %w", initErr)
		}
	}
	repo, err := openLocalRepo(dir)
	if err != nil {
		return err
	}
	checkoutErr := checkoutMainBranch(repo)
	if checkoutErr != nil {
		return checkoutErr
	}
	identityErr := ensureRepoIdentity(repo)
	if identityErr != nil {
		return identityErr
	}
	ctxErr := ensureContextAlive(runCtx)
	if ctxErr != nil {
		return ctxErr
	}
	return nil
}
