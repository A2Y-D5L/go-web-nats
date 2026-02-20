package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func (a *API) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		projects, err := a.store.ListProjects(r.Context())
		if err != nil {
			http.Error(w, "failed to list projects", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, projects)

	case http.MethodPost:
		var spec ProjectSpec
		if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		spec = normalizeProjectSpec(spec)
		if err := validateProjectSpec(spec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		projectID := newID()
		now := time.Now().UTC()

		p := Project{
			ID:        projectID,
			CreatedAt: now,
			UpdatedAt: now,
			Spec:      spec,
			Status: ProjectStatus{
				Phase:      "Reconciling",
				UpdatedAt:  now,
				LastOpID:   "",
				LastOpKind: "",
				Message:    "queued",
			},
		}
		putErr := a.store.PutProject(r.Context(), p)
		if putErr != nil {
			http.Error(w, "failed to persist project", http.StatusInternalServerError)
			return
		}

		op, final, err := a.runOp(r.Context(), OpCreate, projectID, spec)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Return project + last op for the UI
		p, _ = a.store.GetProject(r.Context(), projectID)
		writeJSON(w, http.StatusOK, map[string]any{
			"project": p,
			"op":      op,
			"final":   final,
		})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleProjectByID(w http.ResponseWriter, r *http.Request) {
	projectID, ok := a.resolveProjectIDFromPath(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		a.handleProjectGetByID(w, r, projectID)
	case http.MethodPut:
		a.handleProjectUpdateByID(w, r, projectID)
	case http.MethodDelete:
		a.handleProjectDeleteByID(w, r, projectID)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) resolveProjectIDFromPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !strings.HasPrefix(r.URL.Path, "/api/projects/") {
		http.NotFound(w, r)
		return "", false
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/")
	if rest == "" {
		http.NotFound(w, r)
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) > 1 {
		if parts[1] == "artifacts" {
			a.handleProjectArtifacts(w, r)
			return "", false
		}
		http.NotFound(w, r)
		return "", false
	}
	projectID := strings.TrimSpace(parts[0])
	if projectID == "" {
		http.Error(w, "bad project id", http.StatusBadRequest)
		return "", false
	}
	return projectID, true
}

func (a *API) handleProjectGetByID(w http.ResponseWriter, r *http.Request, projectID string) {
	project, ok := a.getProjectOrWriteError(w, r, projectID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (a *API) handleProjectUpdateByID(w http.ResponseWriter, r *http.Request, projectID string) {
	var spec ProjectSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	spec = normalizeProjectSpec(spec)
	if err := validateProjectSpec(spec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	project, ok := a.getProjectOrWriteError(w, r, projectID)
	if !ok {
		return
	}
	project.Spec = spec
	project.Status.Phase = "Reconciling"
	project.Status.Message = "queued update"
	project.Status.UpdatedAt = time.Now().UTC()
	putErr := a.store.PutProject(r.Context(), project)
	if putErr != nil {
		http.Error(w, "failed to persist project", http.StatusInternalServerError)
		return
	}

	op, final, err := a.runOp(r.Context(), OpUpdate, projectID, spec)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	project, _ = a.store.GetProject(r.Context(), projectID)
	writeJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"op":      op,
		"final":   final,
	})
}

func (a *API) handleProjectDeleteByID(w http.ResponseWriter, r *http.Request, projectID string) {
	project, ok := a.getProjectOrWriteError(w, r, projectID)
	if !ok {
		return
	}
	project.Status.Phase = projectPhaseDel
	project.Status.Message = "queued delete"
	project.Status.UpdatedAt = time.Now().UTC()
	_ = a.store.PutProject(r.Context(), project)

	op, final, err := a.runOp(r.Context(), OpDelete, projectID, zeroProjectSpec())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": true,
		"op":      op,
		"final":   final,
	})
}

func (a *API) getProjectOrWriteError(
	w http.ResponseWriter,
	r *http.Request,
	projectID string,
) (Project, bool) {
	project, err := a.store.GetProject(r.Context(), projectID)
	if err == nil {
		return project, true
	}
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return Project{}, false
	}
	http.Error(w, "failed to read project", http.StatusInternalServerError)
	return Project{}, false
}
