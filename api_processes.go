package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	defaultDeployEnvironment  = "dev"
	defaultReleaseEnvironment = "prod"
)

func (a *API) handleDeploymentEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var evt DeploymentEvent
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	evt.ProjectID = strings.TrimSpace(evt.ProjectID)
	if evt.ProjectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	env := normalizeEnvironmentName(evt.Environment)
	if env == "" {
		env = defaultDeployEnvironment
	}
	if env != defaultDeployEnvironment {
		http.Error(
			w,
			"deployment endpoint supports dev only; use promotion/release for higher environments",
			http.StatusBadRequest,
		)
		return
	}

	project, err := a.store.GetProject(r.Context(), evt.ProjectID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to read project", http.StatusInternalServerError)
		return
	}

	op, final, err := a.runOp(
		r.Context(),
		OpDeploy,
		project.ID,
		project.Spec,
		deployOpRunOptions(env),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	project, _ = a.store.GetProject(r.Context(), project.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"op":      op,
		"final":   final,
	})
}

func (a *API) handlePromotionEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var evt PromotionEvent
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(evt.ProjectID) == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	op, final, project, err := a.runTransitionLifecycle(
		r,
		strings.TrimSpace(evt.ProjectID),
		evt.FromEnv,
		evt.ToEnv,
		false,
	)
	if err != nil {
		writeTransitionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"op":      op,
		"final":   final,
	})
}

func (a *API) handleReleaseEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var evt ReleaseEvent
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(evt.ProjectID) == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	toEnv := evt.ToEnv
	if strings.TrimSpace(toEnv) == "" {
		toEnv = defaultReleaseEnvironment
	}

	op, final, project, err := a.runTransitionLifecycle(
		r,
		strings.TrimSpace(evt.ProjectID),
		evt.FromEnv,
		toEnv,
		true,
	)
	if err != nil {
		writeTransitionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"op":      op,
		"final":   final,
	})
}

type transitionRequestError struct {
	status int
	msg    string
}

func (e transitionRequestError) Error() string {
	return e.msg
}

func writeTransitionError(w http.ResponseWriter, err error) {
	var reqErr transitionRequestError
	if errors.As(err, &reqErr) {
		http.Error(w, reqErr.msg, reqErr.status)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func requestError(status int, msg string) error {
	return transitionRequestError{
		status: status,
		msg:    msg,
	}
}

func (a *API) runTransitionLifecycle(
	r *http.Request,
	projectID string,
	fromEnvRaw string,
	toEnvRaw string,
	releaseOnly bool,
) (Operation, WorkerResultMsg, Project, error) {
	fromEnv := normalizeEnvironmentName(fromEnvRaw)
	toEnv := normalizeEnvironmentName(toEnvRaw)
	if fromEnv == "" || toEnv == "" {
		return Operation{}, WorkerResultMsg{}, Project{}, requestError(
			http.StatusBadRequest,
			"from_env and to_env are required",
		)
	}
	if !isValidEnvironmentName(fromEnv) || !isValidEnvironmentName(toEnv) {
		return Operation{}, WorkerResultMsg{}, Project{}, requestError(
			http.StatusBadRequest,
			"from_env and to_env must be valid environment names",
		)
	}

	project, err := a.store.GetProject(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return Operation{}, WorkerResultMsg{}, Project{}, requestError(http.StatusNotFound, "not found")
		}
		return Operation{}, WorkerResultMsg{}, Project{}, fmt.Errorf("failed to read project: %w", err)
	}
	spec := normalizeProjectSpec(project.Spec)

	resolvedFromEnv, ok := resolveProjectEnvironmentName(spec, fromEnv)
	if !ok {
		return Operation{}, WorkerResultMsg{}, Project{}, requestError(
			http.StatusBadRequest,
			fmt.Sprintf("from_env %q is not defined for project", fromEnv),
		)
	}
	resolvedToEnv, ok := resolveProjectEnvironmentName(spec, toEnv)
	if !ok {
		return Operation{}, WorkerResultMsg{}, Project{}, requestError(
			http.StatusBadRequest,
			fmt.Sprintf("to_env %q is not defined for project", toEnv),
		)
	}
	if resolvedFromEnv == resolvedToEnv {
		return Operation{}, WorkerResultMsg{}, Project{}, requestError(
			http.StatusBadRequest,
			"from_env and to_env must differ",
		)
	}

	stage := transitionDeliveryStage(resolvedToEnv)
	if releaseOnly && stage != DeliveryStageRelease {
		return Operation{}, WorkerResultMsg{}, Project{}, requestError(
			http.StatusBadRequest,
			fmt.Sprintf(
				"release endpoint requires production target environment (got %q)",
				resolvedToEnv,
			),
		)
	}
	kind := transitionOperationKind(stage)

	op, final, err := a.runOp(
		r.Context(),
		kind,
		project.ID,
		spec,
		transitionOpRunOptions(resolvedFromEnv, resolvedToEnv, stage),
	)
	if err != nil {
		return Operation{}, WorkerResultMsg{}, Project{}, err
	}
	latestProject, readErr := a.store.GetProject(r.Context(), project.ID)
	if readErr == nil {
		project = latestProject
	}
	return op, final, project, nil
}

func transitionDeliveryStage(toEnv string) DeliveryStage {
	if isProductionEnvironment(toEnv) {
		return DeliveryStageRelease
	}
	return DeliveryStagePromote
}

func transitionOperationKind(stage DeliveryStage) OperationKind {
	if stage == DeliveryStageRelease {
		return OpRelease
	}
	return OpPromote
}

func normalizeEnvironmentName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func isValidEnvironmentName(name string) bool {
	return envNameRe.MatchString(name)
}

func isProductionEnvironment(name string) bool {
	env := normalizeEnvironmentName(name)
	return env == "prod" || env == "production"
}

func resolveProjectEnvironmentName(spec ProjectSpec, env string) (string, bool) {
	spec = normalizeProjectSpec(spec)
	env = normalizeEnvironmentName(env)
	if env == "" {
		return "", false
	}
	if env == defaultDeployEnvironment {
		return defaultDeployEnvironment, true
	}
	if _, ok := spec.Environments[env]; ok {
		return env, true
	}
	if !isProductionEnvironment(env) {
		return "", false
	}
	for _, candidate := range []string{"prod", "production"} {
		if _, ok := spec.Environments[candidate]; ok {
			return candidate, true
		}
	}
	return "", false
}

func projectSupportsEnvironment(spec ProjectSpec, env string) bool {
	_, ok := resolveProjectEnvironmentName(spec, env)
	return ok
}
