package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

func (a *API) createProjectFromSpec(
	ctx context.Context,
	spec ProjectSpec,
) (Project, Operation, error) {
	spec = normalizeProjectSpec(spec)
	if err := validateProjectSpec(spec); err != nil {
		return Project{}, Operation{}, err
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
			Message:    statusMessageQueued,
		},
	}
	putErr := a.store.PutProject(ctx, p)
	if putErr != nil {
		return Project{}, Operation{}, errors.New("failed to persist project")
	}

	op, err := a.enqueueOp(ctx, OpCreate, projectID, spec, emptyOpRunOptions())
	if err != nil {
		rollbackErr := a.store.DeleteProject(context.WithoutCancel(ctx), projectID)
		return Project{}, Operation{}, withCreateRollbackResult(err, projectID, rollbackErr)
	}
	project, err := a.store.GetProject(ctx, projectID)
	if err != nil {
		return Project{}, Operation{}, err
	}
	return project, op, nil
}

func (a *API) updateProjectFromSpec(
	ctx context.Context,
	projectID string,
	spec ProjectSpec,
) (Project, Operation, error) {
	spec = normalizeProjectSpec(spec)
	if err := validateProjectSpec(spec); err != nil {
		return Project{}, Operation{}, err
	}

	if _, err := a.store.GetProject(ctx, projectID); err != nil {
		return Project{}, Operation{}, err
	}

	op, err := a.enqueueOp(ctx, OpUpdate, projectID, spec, emptyOpRunOptions())
	if err != nil {
		return Project{}, Operation{}, err
	}
	project, _ := a.store.GetProject(ctx, projectID)
	return project, op, nil
}

func (a *API) deleteProject(
	ctx context.Context,
	projectID string,
) (Operation, error) {
	if _, err := a.store.GetProject(ctx, projectID); err != nil {
		return Operation{}, err
	}

	op, err := a.enqueueOp(ctx, OpDelete, projectID, zeroProjectSpec(), emptyOpRunOptions())
	if err != nil {
		return Operation{}, err
	}
	return op, nil
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
	project, op, err := a.createProjectFromSpec(r.Context(), spec)
	if err != nil {
		writeRegistrationError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"project":  project,
		"op":       op,
	})
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
	project, op, err := a.updateProjectFromSpec(r.Context(), projectID, spec)
	if err != nil {
		writeRegistrationError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"project":  project,
		"op":       op,
	})
}

func (a *API) handleRegistrationDelete(w http.ResponseWriter, r *http.Request, projectID string) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	op, err := a.deleteProject(r.Context(), projectID)
	if err != nil {
		writeRegistrationError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":   true,
		"deleted":    false,
		"project_id": projectID,
		"op":         op,
	})
}

func writeRegistrationError(w http.ResponseWriter, err error) {
	if writeAsyncOpError(w, err) {
		return
	}
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
