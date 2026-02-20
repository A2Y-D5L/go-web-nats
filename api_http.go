package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

////////////////////////////////////////////////////////////////////////////////
// HTTP API + UI
////////////////////////////////////////////////////////////////////////////////

type API struct {
	nc        *nats.Conn
	store     *Store
	artifacts ArtifactStore
	waiters   *waiterHub
}

func (a *API) routes() http.Handler {
	mux := http.NewServeMux()

	// Static UI
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// CRUD: projects
	mux.HandleFunc("/api/projects", a.handleProjects)
	mux.HandleFunc("/api/projects/", a.handleProjectByID)
	mux.HandleFunc("/api/events/registration", a.handleRegistrationEvents)
	mux.HandleFunc("/api/webhooks/source", a.handleSourceRepoWebhook)

	// Ops: read
	mux.HandleFunc("/api/ops/", a.handleOpByID)

	return a.withRequestLogging(mux)
}

type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(p)
}

func (a *API) withRequestLogging(next http.Handler) http.Handler {
	apiLog := appLoggerForProcess().Source("api")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{
			ResponseWriter: w,
			status:         0,
		}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		dur := time.Since(started).Round(time.Millisecond)
		msg := fmt.Sprintf("%s %s -> %d (%s)", r.Method, r.URL.Path, rec.status, dur)
		switch {
		case rec.status >= httpServerErrThreshold:
			apiLog.Errorf("%s", msg)
		case rec.status >= httpClientErrThreshold:
			apiLog.Warnf("%s", msg)
		default:
			apiLog.Infof("%s", msg)
		}
	})
}

type RegistrationEvent struct {
	Action    string      `json:"action"` // create|update|delete
	ProjectID string      `json:"project_id,omitempty"`
	Spec      ProjectSpec `json:"spec"`
}

type SourceRepoWebhookEvent struct {
	ProjectID string `json:"project_id"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Ref       string `json:"ref,omitempty"` // e.g. refs/heads/main
	Commit    string `json:"commit,omitempty"`
}

func (a *API) createProjectFromSpec(
	ctx context.Context,
	spec ProjectSpec,
) (Project, Operation, WorkerResultMsg, error) {
	spec = normalizeProjectSpec(spec)
	if err := validateProjectSpec(spec); err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
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
	putErr := a.store.PutProject(ctx, p)
	if putErr != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, errors.New("failed to persist project")
	}

	op, final, err := a.runOp(ctx, OpCreate, projectID, spec)
	if err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
	}
	p, _ = a.store.GetProject(ctx, projectID)
	return p, op, final, nil
}

func (a *API) updateProjectFromSpec(
	ctx context.Context,
	projectID string,
	spec ProjectSpec,
) (Project, Operation, WorkerResultMsg, error) {
	spec = normalizeProjectSpec(spec)
	if err := validateProjectSpec(spec); err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
	}

	p, err := a.store.GetProject(ctx, projectID)
	if err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
	}
	p.Spec = spec
	p.Status.Phase = "Reconciling"
	p.Status.Message = "queued update"
	p.Status.UpdatedAt = time.Now().UTC()
	putErr := a.store.PutProject(ctx, p)
	if putErr != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, errors.New("failed to persist project")
	}

	op, final, err := a.runOp(ctx, OpUpdate, projectID, spec)
	if err != nil {
		return Project{}, Operation{}, WorkerResultMsg{}, err
	}
	p, _ = a.store.GetProject(ctx, projectID)
	return p, op, final, nil
}

func (a *API) deleteProject(
	ctx context.Context,
	projectID string,
) (Operation, WorkerResultMsg, error) {
	p, err := a.store.GetProject(ctx, projectID)
	if err != nil {
		return Operation{}, WorkerResultMsg{}, err
	}
	p.Status.Phase = projectPhaseDel
	p.Status.Message = "queued delete"
	p.Status.UpdatedAt = time.Now().UTC()
	_ = a.store.PutProject(ctx, p)

	op, final, err := a.runOp(ctx, OpDelete, projectID, zeroProjectSpec())
	if err != nil {
		return Operation{}, WorkerResultMsg{}, err
	}
	return op, final, nil
}

func (a *API) handleRegistrationEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	evt, err := decodeRegistrationEvent(r)
	if err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	switch evt.Action {
	case "create":
		a.handleRegistrationCreate(w, r, evt.Spec)
	case "update":
		a.handleRegistrationUpdate(w, r, evt.ProjectID, evt.Spec)
	case "delete":
		a.handleRegistrationDelete(w, r, evt.ProjectID)
	default:
		http.Error(w, "action must be create, update, or delete", http.StatusBadRequest)
	}
}

func decodeRegistrationEvent(r *http.Request) (RegistrationEvent, error) {
	var evt RegistrationEvent
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		return RegistrationEvent{}, err
	}
	evt.Action = strings.TrimSpace(strings.ToLower(evt.Action))
	return evt, nil
}

func (a *API) handleRegistrationCreate(w http.ResponseWriter, r *http.Request, spec ProjectSpec) {
	project, op, final, err := a.createProjectFromSpec(r.Context(), spec)
	if err != nil {
		writeRegistrationError(w, err)
		return
	}
	writeProjectOpFinalResponse(w, project, op, final)
}

func (a *API) handleRegistrationUpdate(
	w http.ResponseWriter,
	r *http.Request,
	projectID string,
	spec ProjectSpec,
) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	project, op, final, err := a.updateProjectFromSpec(r.Context(), projectID, spec)
	if err != nil {
		writeRegistrationError(w, err)
		return
	}
	writeProjectOpFinalResponse(w, project, op, final)
}

func (a *API) handleRegistrationDelete(w http.ResponseWriter, r *http.Request, projectID string) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	op, final, err := a.deleteProject(r.Context(), projectID)
	if err != nil {
		writeRegistrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": true,
		"op":      op,
		"final":   final,
	})
}

func writeProjectOpFinalResponse(
	w http.ResponseWriter,
	project Project,
	op Operation,
	final WorkerResultMsg,
) {
	writeJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"op":      op,
		"final":   final,
	})
}

func writeRegistrationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, jetstream.ErrKeyNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case isValidationError(err):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func isValidationError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "must") || strings.Contains(msg, "invalid")
}

func normalizeBranchValue(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.TrimPrefix(v, "refs/heads/")
	v = strings.TrimPrefix(v, "heads/")
	return v
}

func isMainBranchWebhook(branch, ref string) bool {
	// Support either plain branch names ("main") or refs ("refs/heads/main")
	// from webhook providers and accept either field if present.
	return normalizeBranchValue(branch) == branchMain || normalizeBranchValue(ref) == branchMain
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

func (a *API) runOp(
	ctx context.Context,
	kind OperationKind,
	projectID string,
	spec ProjectSpec,
) (Operation, WorkerResultMsg, error) {
	apiLog := appLoggerForProcess().Source("api")
	opID := newID()
	now := time.Now().UTC()

	// Persist initial op record
	op := Operation{
		ID:        opID,
		Kind:      kind,
		ProjectID: projectID,
		Requested: now,
		Finished:  time.Time{},
		Status:    "queued",
		Error:     "",
		Steps:     []OpStep{},
	}
	if err := a.store.PutOp(ctx, op); err != nil {
		return Operation{}, WorkerResultMsg{}, fmt.Errorf("persist op: %w", err)
	}
	apiLog.Infof("queued op=%s kind=%s project=%s", opID, kind, projectID)

	// Update project status (except delete might be removed later)
	if kind != OpDelete {
		p, err := a.store.GetProject(ctx, projectID)
		if err == nil {
			p.Spec = spec
			msg := "queued"
			if kind == OpCI {
				msg = "queued ci from source webhook"
			}
			p.Status = ProjectStatus{
				Phase:      "Reconciling",
				UpdatedAt:  now,
				LastOpID:   opID,
				LastOpKind: string(kind),
				Message:    msg,
			}
			_ = a.store.PutProject(ctx, p)
		}
	} else {
		_ = finalizeOp(ctx, a.store, opID, projectID, kind, "running", "")
	}

	// Register waiter before publish
	ch := a.waiters.register(opID)
	defer a.waiters.unregister(opID)

	// Publish start message
	msg := ProjectOpMsg{
		OpID:      opID,
		Kind:      kind,
		ProjectID: projectID,
		Spec:      spec,
		Err:       "",
		At:        now,
	}
	b, _ := json.Marshal(msg)
	startSubject := subjectProjectOpStart
	if kind == OpCI {
		startSubject = subjectBootstrapDone
	}
	finalizeCtx := context.WithoutCancel(ctx)
	if err := a.nc.Publish(startSubject, b); err != nil {
		_ = finalizeOp(finalizeCtx, a.store, opID, projectID, kind, "error", err.Error())
		apiLog.Errorf("publish failed op=%s kind=%s project=%s: %v", opID, kind, projectID, err)
		return Operation{}, WorkerResultMsg{}, fmt.Errorf("publish op: %w", err)
	}
	apiLog.Debugf("published op=%s subject=%s", opID, startSubject)

	// Wait for final worker completion
	waitCtx, cancel := context.WithTimeout(ctx, apiWaitTimeout)
	defer cancel()

	var final WorkerResultMsg
	select {
	case <-waitCtx.Done():
		_ = finalizeOp(
			finalizeCtx,
			a.store,
			opID,
			projectID,
			kind,
			"error",
			"timeout waiting for workers",
		)
		apiLog.Errorf("timeout op=%s kind=%s project=%s", opID, kind, projectID)
		return Operation{}, WorkerResultMsg{}, errors.New("timeout waiting for workers")
	case final = <-ch:
	}

	if final.Err != "" {
		_ = finalizeOp(finalizeCtx, a.store, opID, projectID, kind, "error", final.Err)
		apiLog.Errorf("op=%s failed in %s: %s", opID, final.Worker, final.Err)
		return Operation{}, final, errors.New(final.Err)
	}

	// Fetch final op state for response
	op, _ = a.store.GetOp(ctx, opID)
	apiLog.Infof("completed op=%s kind=%s project=%s", opID, kind, projectID)
	return op, final, nil
}
