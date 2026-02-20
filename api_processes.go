package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nats-io/nats.go/jetstream"
)

const defaultDeployEnvironment = "dev"

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
			"deployment endpoint supports dev only; use promotion for higher environments",
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
	evt.ProjectID = strings.TrimSpace(evt.ProjectID)
	if evt.ProjectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	fromEnv := normalizeEnvironmentName(evt.FromEnv)
	toEnv := normalizeEnvironmentName(evt.ToEnv)
	if fromEnv == "" || toEnv == "" {
		http.Error(w, "from_env and to_env are required", http.StatusBadRequest)
		return
	}
	if !isValidEnvironmentName(fromEnv) || !isValidEnvironmentName(toEnv) {
		http.Error(w, "from_env and to_env must be valid environment names", http.StatusBadRequest)
		return
	}
	if fromEnv == toEnv {
		http.Error(w, "from_env and to_env must differ", http.StatusBadRequest)
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
	spec := normalizeProjectSpec(project.Spec)
	if !projectSupportsEnvironment(spec, fromEnv) {
		http.Error(w, fmt.Sprintf("from_env %q is not defined for project", fromEnv), http.StatusBadRequest)
		return
	}
	if !projectSupportsEnvironment(spec, toEnv) {
		http.Error(w, fmt.Sprintf("to_env %q is not defined for project", toEnv), http.StatusBadRequest)
		return
	}

	op, final, err := a.runOp(
		r.Context(),
		OpPromote,
		project.ID,
		spec,
		promotionOpRunOptions(fromEnv, toEnv),
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

func normalizeEnvironmentName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func isValidEnvironmentName(name string) bool {
	return envNameRe.MatchString(name)
}

func projectSupportsEnvironment(spec ProjectSpec, env string) bool {
	spec = normalizeProjectSpec(spec)
	if env == defaultDeployEnvironment {
		return true
	}
	_, ok := spec.Environments[env]
	return ok
}
