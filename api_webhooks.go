package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

const sourceRepoLastCICommitPath = "repos/source/.paas/last-ci-commit.txt"

type sourceRepoWebhookResult struct {
	accepted bool
	reason   string
	project  string
	op       *Operation
	commit   string
	trigger  string
}

func (a *API) handleSourceRepoWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var evt SourceRepoWebhookEvent
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	evt.ProjectID = strings.TrimSpace(evt.ProjectID)
	if evt.ProjectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	result, err := a.triggerSourceRepoCI(r.Context(), evt, "source.main.webhook")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if result.reason == "project not found" {
		http.Error(w, result.reason, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": result.accepted,
		"reason":   result.reason,
		"trigger":  result.trigger,
		"project":  result.project,
		"op":       result.op,
		"commit":   result.commit,
	})
}

func (a *API) triggerSourceRepoCI(
	ctx context.Context,
	evt SourceRepoWebhookEvent,
	trigger string,
) (sourceRepoWebhookResult, error) {
	if evt.Repo != "" && strings.ToLower(strings.TrimSpace(evt.Repo)) != "source" {
		return sourceRepoWebhookResult{
			accepted: false,
			reason:   "ignored: only source repo webhooks trigger ci",
			project:  evt.ProjectID,
			op:       nil,
			commit:   strings.TrimSpace(evt.Commit),
			trigger:  trigger,
		}, nil
	}
	if !isMainBranchWebhook(evt.Branch, evt.Ref) {
		return sourceRepoWebhookResult{
			accepted: false,
			reason:   "ignored: only main branch triggers CI",
			project:  evt.ProjectID,
			op:       nil,
			commit:   strings.TrimSpace(evt.Commit),
			trigger:  trigger,
		}, nil
	}

	project, err := a.store.GetProject(ctx, evt.ProjectID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return sourceRepoWebhookResult{
				accepted: false,
				reason:   "project not found",
				project:  evt.ProjectID,
				op:       nil,
				commit:   strings.TrimSpace(evt.Commit),
				trigger:  trigger,
			}, nil
		}
		return sourceRepoWebhookResult{}, err
	}

	a.sourceTriggerMu.Lock()
	isNewCommit, markErr := a.markSourceCommitSeen(project.ID, evt.Commit)
	a.sourceTriggerMu.Unlock()
	if markErr != nil {
		return sourceRepoWebhookResult{}, markErr
	}
	if !isNewCommit {
		return sourceRepoWebhookResult{
			accepted: false,
			reason:   "ignored: commit already processed",
			project:  project.ID,
			op:       nil,
			commit:   strings.TrimSpace(evt.Commit),
			trigger:  trigger,
		}, nil
	}

	op, _, err := a.runOp(ctx, OpCI, project.ID, project.Spec, emptyOpRunOptions())
	if err != nil {
		return sourceRepoWebhookResult{}, err
	}
	return sourceRepoWebhookResult{
		accepted: true,
		reason:   "",
		project:  project.ID,
		op:       &op,
		commit:   strings.TrimSpace(evt.Commit),
		trigger:  trigger,
	}, nil
}

func (a *API) markSourceCommitSeen(projectID, commit string) (bool, error) {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return true, nil
	}
	data, err := a.artifacts.ReadFile(projectID, sourceRepoLastCICommitPath)
	if err == nil && strings.TrimSpace(string(data)) == commit {
		return false, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	_, err = a.artifacts.WriteFile(projectID, sourceRepoLastCICommitPath, []byte(commit+"\n"))
	if err != nil {
		return false, err
	}
	return true, nil
}

func shouldSkipSourceCommitMessage(message string) bool {
	return strings.HasPrefix(strings.TrimSpace(message), platformSyncPrefix)
}
