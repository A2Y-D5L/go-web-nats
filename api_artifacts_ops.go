package platform

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type projectOpsListItem struct {
	ID                string        `json:"id"`
	Kind              OperationKind `json:"kind"`
	Status            string        `json:"status"`
	Requested         time.Time     `json:"requested"`
	Finished          time.Time     `json:"finished"`
	Error             string        `json:"error,omitempty"`
	SummaryMessage    string        `json:"summary_message,omitempty"`
	LastEventSequence int64         `json:"last_event_sequence"`
	LastUpdateAt      time.Time     `json:"last_update_at"`
}

type projectOpsListResponse struct {
	Items      []projectOpsListItem `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

func (a *API) handleProjectArtifacts(w http.ResponseWriter, r *http.Request) {
	// Routes:
	//  - GET /api/projects/{id}/artifacts              -> list files
	//  - GET /api/projects/{id}/artifacts/{path...}    -> download file
	if !strings.HasPrefix(r.URL.Path, "/api/projects/") {
		http.NotFound(w, r)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[1] != "artifacts" {
		http.NotFound(w, r)
		return
	}

	projectID := strings.TrimSpace(parts[0])
	if projectID == "" {
		http.Error(w, "bad project id", http.StatusBadRequest)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// list
	if len(parts) == projectRelPathPartsMin {
		files, err := a.artifacts.ListFiles(projectID)
		if err != nil {
			http.Error(w, "failed to list artifacts", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"files": files})
		return
	}

	// download
	relPath := strings.Join(parts[2:], "/")
	relPath = strings.TrimPrefix(relPath, "/")
	data, err := a.artifacts.ReadFile(projectID, relPath)
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to read artifact", http.StatusInternalServerError)
		return
	}

	// Minimal content type handling
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().
		Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(relPath)))
	http.ServeContent(w, r, filepath.Base(relPath), time.Time{}, bytes.NewReader(data))
}

func (a *API) handleProjectOps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.store == nil {
		http.Error(w, "operation data unavailable", http.StatusInternalServerError)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/projects/") {
		http.NotFound(w, r)
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != projectRelPathPartsMin || parts[1] != "ops" {
		http.NotFound(w, r)
		return
	}

	projectID := strings.TrimSpace(parts[0])
	if projectID == "" {
		http.Error(w, "bad project id", http.StatusBadRequest)
		return
	}
	if _, ok := a.getProjectOrWriteError(w, r, projectID); !ok {
		return
	}

	limit, err := parseProjectOpsLimitParam(r.URL.Query().Get("limit"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	page, err := a.store.listProjectOps(
		r.Context(),
		projectID,
		projectOpsListQuery{
			Limit:  limit,
			Cursor: r.URL.Query().Get("cursor"),
			Before: r.URL.Query().Get("before"),
		},
	)
	if err != nil {
		http.Error(w, "failed to list operations", http.StatusInternalServerError)
		return
	}

	items := make([]projectOpsListItem, 0, len(page.Ops))
	for _, op := range page.Ops {
		items = append(items, projectOpsListItem{
			ID:                op.ID,
			Kind:              op.Kind,
			Status:            op.Status,
			Requested:         op.Requested,
			Finished:          op.Finished,
			Error:             op.Error,
			SummaryMessage:    opSummaryMessage(op),
			LastEventSequence: a.store.latestOpEventSequence(op.ID),
			LastUpdateAt:      opLastUpdateAt(op),
		})
	}

	writeJSON(w, http.StatusOK, projectOpsListResponse{
		Items:      items,
		NextCursor: page.NextCursor,
	})
}

func (a *API) handleOpByID(w http.ResponseWriter, r *http.Request) {
	// GET /api/ops/{id}
	// GET /api/ops/{id}/events
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/ops/") {
		http.NotFound(w, r)
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/ops/"), "/")
	if rest == "" {
		http.Error(w, "bad op id", http.StatusBadRequest)
		return
	}
	parts := strings.Split(rest, "/")
	opID := strings.TrimSpace(parts[0])
	if opID == "" {
		http.Error(w, "bad op id", http.StatusBadRequest)
		return
	}
	if len(parts) == 2 && parts[1] == "events" {
		a.handleOpEvents(w, r, opID)
		return
	}
	if len(parts) != 1 {
		http.NotFound(w, r)
		return
	}
	if a.store == nil {
		http.Error(w, "operation data unavailable", http.StatusInternalServerError)
		return
	}

	op, err := a.store.GetOp(r.Context(), opID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to read op", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, op)
}

func parseProjectOpsLimitParam(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return projectOpsDefaultLimit, nil
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil || parsed <= 0 {
		return 0, errors.New("bad limit")
	}
	return normalizeProjectOpsLimit(parsed), nil
}

func opSummaryMessage(op Operation) string {
	for idx := len(op.Steps) - 1; idx >= 0; idx-- {
		msg := strings.TrimSpace(op.Steps[idx].Message)
		if msg != "" {
			return msg
		}
	}

	switch strings.TrimSpace(op.Status) {
	case statusMessageQueued:
		return "operation accepted and queued"
	case opStatusRunning:
		return "operation in progress"
	case opStatusDone:
		return opMessageDone
	case opStatusError:
		if errMsg := strings.TrimSpace(op.Error); errMsg != "" {
			return errMsg
		}
		return opMessageFailed
	default:
		return ""
	}
}

func opLastUpdateAt(op Operation) time.Time {
	if !op.Finished.IsZero() {
		return op.Finished.UTC()
	}
	for idx := len(op.Steps) - 1; idx >= 0; idx-- {
		if !op.Steps[idx].EndedAt.IsZero() {
			return op.Steps[idx].EndedAt.UTC()
		}
		if !op.Steps[idx].StartedAt.IsZero() {
			return op.Steps[idx].StartedAt.UTC()
		}
	}
	if !op.Requested.IsZero() {
		return op.Requested.UTC()
	}
	return time.Now().UTC()
}
