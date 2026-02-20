package platform

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

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

func (a *API) handleOpByID(w http.ResponseWriter, r *http.Request) {
	// GET /api/ops/{id}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	opID := strings.TrimPrefix(r.URL.Path, "/api/ops/")
	opID = strings.TrimSpace(opID)
	if opID == "" || strings.Contains(opID, "/") {
		http.Error(w, "bad op id", http.StatusBadRequest)
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
