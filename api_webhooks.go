package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	sourceRepoCICommitStatePath         = "repos/source/.paas/ci-commit-state.json"
	sourceRepoLastCICommitLegacyPath    = "repos/source/.paas/last-ci-commit.txt"
	sourceRepoCIPendingStatusEnqueued   = "enqueued"
	sourceRepoCIPendingStatusFailed     = "failed"
	sourceRepoWebhookCommitIgnoredLabel = "ignored: commit already processed"
)

type sourceRepoCICommitPendingState struct {
	Commit string `json:"commit"`
	Status string `json:"status"` // enqueued | failed
}

type sourceRepoCICommitState struct {
	LastSuccessfulCommit string                                    `json:"last_successful_commit,omitempty"`
	PendingEnqueueCommit string                                    `json:"pending_enqueue_commit,omitempty"`
	PendingByOpID        map[string]sourceRepoCICommitPendingState `json:"pending_by_op_id,omitempty"`
}

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
		if writeAsyncOpError(w, err) {
			return
		}
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
	defer a.sourceTriggerMu.Unlock()

	isNewCommit, markErr := a.markSourceCommitSeen(project.ID, evt.Commit)
	if markErr != nil {
		return sourceRepoWebhookResult{}, markErr
	}
	if !isNewCommit {
		return sourceRepoWebhookResult{
			accepted: false,
			reason:   sourceRepoWebhookCommitIgnoredLabel,
			project:  project.ID,
			op:       nil,
			commit:   strings.TrimSpace(evt.Commit),
			trigger:  trigger,
		}, nil
	}

	op, err := a.enqueueOp(ctx, OpCI, project.ID, project.Spec, emptyOpRunOptions())
	if err != nil {
		rollbackErr := a.rollbackSourceCommitPendingEnqueue(project.ID, evt.Commit)
		if rollbackErr != nil {
			return sourceRepoWebhookResult{}, errors.Join(err, rollbackErr)
		}
		return sourceRepoWebhookResult{}, err
	}
	confirmErr := a.confirmSourceCommitPendingOp(project.ID, evt.Commit, op.ID)
	if confirmErr != nil {
		appLoggerForProcess().Source("api").Warnf(
			"project=%s op=%s commit=%s persist ci pending state: %v",
			project.ID,
			op.ID,
			shortID(strings.TrimSpace(evt.Commit)),
			confirmErr,
		)
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

	state, err := readSourceRepoCICommitState(a.artifacts, projectID)
	if err != nil {
		return false, err
	}
	if state.LastSuccessfulCommit == commit || state.hasPendingCommit(commit) {
		return false, nil
	}

	state.PendingEnqueueCommit = commit
	writeErr := writeSourceRepoCICommitState(a.artifacts, projectID, state)
	if writeErr != nil {
		return false, writeErr
	}
	return true, nil
}

func (a *API) rollbackSourceCommitPendingEnqueue(projectID, commit string) error {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return nil
	}
	state, err := readSourceRepoCICommitState(a.artifacts, projectID)
	if err != nil {
		return err
	}
	if state.PendingEnqueueCommit != commit {
		return nil
	}
	state.PendingEnqueueCommit = ""
	return writeSourceRepoCICommitState(a.artifacts, projectID, state)
}

func (a *API) confirmSourceCommitPendingOp(projectID, commit, opID string) error {
	commit = strings.TrimSpace(commit)
	opID = strings.TrimSpace(opID)
	if commit == "" || opID == "" {
		return nil
	}
	state, err := readSourceRepoCICommitState(a.artifacts, projectID)
	if err != nil {
		return err
	}
	if state.PendingEnqueueCommit == commit {
		state.PendingEnqueueCommit = ""
	}
	if state.PendingByOpID == nil {
		state.PendingByOpID = map[string]sourceRepoCICommitPendingState{}
	}
	for pendingOpID, pendingState := range state.PendingByOpID {
		if pendingState.Commit == commit && pendingState.Status == sourceRepoCIPendingStatusFailed {
			delete(state.PendingByOpID, pendingOpID)
		}
	}
	state.PendingByOpID[opID] = sourceRepoCICommitPendingState{
		Commit: commit,
		Status: sourceRepoCIPendingStatusEnqueued,
	}
	return writeSourceRepoCICommitState(a.artifacts, projectID, state)
}

func finalizeSourceCommitPendingOp(
	artifacts ArtifactStore,
	projectID, opID string,
	successful bool,
) error {
	opID = strings.TrimSpace(opID)
	if opID == "" {
		return nil
	}
	state, err := readSourceRepoCICommitState(artifacts, projectID)
	if err != nil {
		return err
	}
	pendingState, ok := state.PendingByOpID[opID]
	if !ok {
		return nil
	}
	if !successful {
		pendingState.Status = sourceRepoCIPendingStatusFailed
		state.PendingByOpID[opID] = pendingState
		return writeSourceRepoCICommitState(artifacts, projectID, state)
	}

	state.LastSuccessfulCommit = pendingState.Commit
	delete(state.PendingByOpID, opID)
	for pendingOpID, existing := range state.PendingByOpID {
		if existing.Commit == pendingState.Commit && existing.Status == sourceRepoCIPendingStatusFailed {
			delete(state.PendingByOpID, pendingOpID)
		}
	}
	return writeSourceRepoCICommitState(artifacts, projectID, state)
}

func (state sourceRepoCICommitState) hasPendingCommit(commit string) bool {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return false
	}
	for _, pending := range state.PendingByOpID {
		if pending.Commit == commit && pending.Status == sourceRepoCIPendingStatusEnqueued {
			return true
		}
	}
	return false
}

func readSourceRepoCICommitState(
	artifacts ArtifactStore,
	projectID string,
) (sourceRepoCICommitState, error) {
	data, err := artifacts.ReadFile(projectID, sourceRepoCICommitStatePath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return sourceRepoCICommitState{}, err
		}
		return readSourceRepoCICommitLegacyState(artifacts, projectID)
	}

	var state sourceRepoCICommitState
	decodeErr := json.Unmarshal(data, &state)
	if decodeErr != nil {
		return sourceRepoCICommitState{}, fmt.Errorf("decode source repo ci state: %w", decodeErr)
	}
	return normalizeSourceRepoCICommitState(state), nil
}

func readSourceRepoCICommitLegacyState(
	artifacts ArtifactStore,
	projectID string,
) (sourceRepoCICommitState, error) {
	data, err := artifacts.ReadFile(projectID, sourceRepoLastCICommitLegacyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sourceRepoCICommitState{}, nil
		}
		return sourceRepoCICommitState{}, err
	}
	return sourceRepoCICommitState{
		LastSuccessfulCommit: strings.TrimSpace(string(data)),
		PendingEnqueueCommit: "",
		PendingByOpID:        nil,
	}, nil
}

func writeSourceRepoCICommitState(
	artifacts ArtifactStore,
	projectID string,
	state sourceRepoCICommitState,
) error {
	state = normalizeSourceRepoCICommitState(state)
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = artifacts.WriteFile(projectID, sourceRepoCICommitStatePath, append(body, '\n'))
	return err
}

func normalizeSourceRepoCICommitState(state sourceRepoCICommitState) sourceRepoCICommitState {
	state.LastSuccessfulCommit = strings.TrimSpace(state.LastSuccessfulCommit)
	state.PendingEnqueueCommit = strings.TrimSpace(state.PendingEnqueueCommit)
	if len(state.PendingByOpID) == 0 {
		state.PendingByOpID = nil
		return state
	}
	normalizedPending := make(map[string]sourceRepoCICommitPendingState, len(state.PendingByOpID))
	for opID, pending := range state.PendingByOpID {
		normalizedOpID := strings.TrimSpace(opID)
		normalizedCommit := strings.TrimSpace(pending.Commit)
		normalizedStatus := normalizeSourceRepoCIPendingStatus(pending.Status)
		if normalizedOpID == "" || normalizedCommit == "" || normalizedStatus == "" {
			continue
		}
		normalizedPending[normalizedOpID] = sourceRepoCICommitPendingState{
			Commit: normalizedCommit,
			Status: normalizedStatus,
		}
	}
	if len(normalizedPending) == 0 {
		state.PendingByOpID = nil
		return state
	}
	state.PendingByOpID = normalizedPending
	return state
}

func normalizeSourceRepoCIPendingStatus(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case sourceRepoCIPendingStatusEnqueued:
		return sourceRepoCIPendingStatusEnqueued
	case sourceRepoCIPendingStatusFailed:
		return sourceRepoCIPendingStatusFailed
	default:
		return ""
	}
}

func shouldSkipSourceCommitMessage(message string) bool {
	return strings.HasPrefix(strings.TrimSpace(message), platformSyncPrefix)
}
