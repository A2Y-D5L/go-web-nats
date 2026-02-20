package main

import (
	"context"
	"fmt"
	"path/filepath"
)

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
