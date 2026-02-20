package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

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
	if evt.Repo != "" && strings.ToLower(strings.TrimSpace(evt.Repo)) != "source" {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": false,
			"reason":   "ignored: only source repo webhooks trigger ci",
		})
		return
	}
	if !isMainBranchWebhook(evt.Branch, evt.Ref) {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": false,
			"reason":   "ignored: only main branch triggers CI",
		})
		return
	}

	p, err := a.store.GetProject(r.Context(), evt.ProjectID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to read project", http.StatusInternalServerError)
		return
	}

	op, _, err := a.runOp(r.Context(), OpCI, p.ID, p.Spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"trigger":  "source.main.webhook",
		"project":  p.ID,
		"op":       op,
		"commit":   evt.Commit,
	})
}
