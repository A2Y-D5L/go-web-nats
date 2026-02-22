package platform

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
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
				Message:    statusMessageQueued,
			},
		}
		putErr := a.store.PutProject(r.Context(), p)
		if putErr != nil {
			http.Error(w, "failed to persist project", http.StatusInternalServerError)
			return
		}

		op, err := a.enqueueOp(r.Context(), OpCreate, projectID, spec, emptyOpRunOptions())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Return project + last op for the UI
		p, _ = a.store.GetProject(r.Context(), projectID)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": true,
			"project":  p,
			"op":       op,
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
		if parts[1] == "journey" {
			a.handleProjectJourney(w, r)
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

	if _, ok := a.getProjectOrWriteError(w, r, projectID); !ok {
		return
	}

	op, err := a.enqueueOp(r.Context(), OpUpdate, projectID, spec, emptyOpRunOptions())
	if err != nil {
		if writeProjectOpConflict(w, err) {
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	project, _ := a.store.GetProject(r.Context(), projectID)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"project":  project,
		"op":       op,
	})
}

func (a *API) handleProjectDeleteByID(w http.ResponseWriter, r *http.Request, projectID string) {
	if _, ok := a.getProjectOrWriteError(w, r, projectID); !ok {
		return
	}

	op, err := a.enqueueOp(
		r.Context(),
		OpDelete,
		projectID,
		zeroProjectSpec(),
		emptyOpRunOptions(),
	)
	if err != nil {
		if writeProjectOpConflict(w, err) {
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":   true,
		"deleted":    false,
		"project_id": projectID,
		"op":         op,
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

type projectJourney struct {
	Summary        string                     `json:"summary"`
	Milestones     []projectJourneyMilestone  `json:"milestones"`
	Environments   []projectJourneyEnv        `json:"environments"`
	NextAction     projectJourneyNextAction   `json:"next_action"`
	ArtifactStats  projectJourneyArtifactStat `json:"artifact_stats"`
	RecentOp       *Operation                 `json:"recent_operation,omitempty"`
	LastUpdateTime time.Time                  `json:"last_update_time"`
}

type projectJourneyMilestone struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // complete | in_progress | pending | blocked | failed
	Detail string `json:"detail"`
}

type projectJourneyEnv struct {
	Name         string `json:"name"`
	State        string `json:"state"` // live | pending
	Image        string `json:"image,omitempty"`
	ImageSource  string `json:"image_source,omitempty"`
	DeliveryType string `json:"delivery_type,omitempty"` // deploy | promote | release
	DeliveryPath string `json:"delivery_path,omitempty"`
	Detail       string `json:"detail"`
}

type projectJourneyNextAction struct {
	Kind        string `json:"kind"` // build | deploy_dev | promote | release | investigate | none
	Label       string `json:"label"`
	Detail      string `json:"detail"`
	Environment string `json:"environment,omitempty"`
	FromEnv     string `json:"from_env,omitempty"`
	ToEnv       string `json:"to_env,omitempty"`
}

type projectJourneyArtifactStat struct {
	Total        int `json:"total"`
	Build        int `json:"build"`
	Deploy       int `json:"deploy"`
	Promotion    int `json:"promotion"`
	Release      int `json:"release"`
	Repository   int `json:"repository"`
	Registration int `json:"registration"`
	Other        int `json:"other"`
}

type transitionArtifact struct {
	action string
	from   string
	to     string
	path   string
}

const (
	journeyStatusComplete    = "complete"
	journeyStatusInProgress  = "in_progress"
	journeyStatusPending     = "pending"
	journeyStatusBlocked     = "blocked"
	journeyStatusFailed      = "failed"
	journeyEnvStateLive      = "live"
	journeyEnvStatePending   = "pending"
	journeyPhaseReconciling  = "Reconciling"
	journeyPathPartsExpected = 2

	journeyRankDev     = 10
	journeyRankStaging = 40
	journeyRankOther   = 50
	journeyRankProd    = 90
)

func (a *API) handleProjectJourney(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.store == nil || a.artifacts == nil {
		http.Error(w, "journey data unavailable", http.StatusInternalServerError)
		return
	}

	if !strings.HasPrefix(r.URL.Path, "/api/projects/") {
		http.NotFound(w, r)
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != journeyPathPartsExpected || parts[1] != "journey" {
		http.NotFound(w, r)
		return
	}

	projectID := strings.TrimSpace(parts[0])
	if projectID == "" {
		http.Error(w, "bad project id", http.StatusBadRequest)
		return
	}

	project, ok := a.getProjectOrWriteError(w, r, projectID)
	if !ok {
		return
	}
	files, err := a.artifacts.ListFiles(projectID)
	if err != nil {
		http.Error(w, "failed to list artifacts", http.StatusInternalServerError)
		return
	}

	journey, err := a.buildProjectJourney(r.Context(), project, files)
	if err != nil {
		http.Error(w, "failed to build project journey", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project": project,
		"journey": journey,
	})
}

func (a *API) buildProjectJourney(
	ctx context.Context,
	project Project,
	files []string,
) (projectJourney, error) {
	fileSet := make(map[string]struct{}, len(files))
	for _, path := range files {
		fileSet[path] = struct{}{}
	}

	buildImage := ""
	if hasPath(fileSet, "build/image.txt") {
		image, err := a.readArtifactTrimmed(project.ID, "build/image.txt")
		if err != nil {
			return projectJourney{}, err
		}
		buildImage = image
	}

	orderedEnvs := journeyEnvironmentOrder(project.Spec)
	transitions := collectTransitionArtifacts(files)

	envs := make([]projectJourneyEnv, 0, len(orderedEnvs))
	for _, env := range orderedEnvs {
		envSummary, err := a.buildJourneyEnvironment(project, env, buildImage, fileSet, transitions)
		if err != nil {
			return projectJourney{}, err
		}
		envs = append(envs, envSummary)
	}

	artifactStats := summarizeArtifacts(files)
	recentOp, foundRecentOp, err := a.readRecentOp(ctx, project.Status.LastOpID)
	if err != nil {
		return projectJourney{}, err
	}

	var recentOpPtr *Operation
	if foundRecentOp {
		recentOpCopy := recentOp
		recentOpPtr = &recentOpCopy
	}

	milestones := buildJourneyMilestones(project, buildImage, envs)
	next := recommendJourneyAction(project, buildImage, envs)

	return projectJourney{
		Summary:        describeJourneySummary(project, buildImage, envs),
		Milestones:     milestones,
		Environments:   envs,
		NextAction:     next,
		ArtifactStats:  artifactStats,
		RecentOp:       recentOpPtr,
		LastUpdateTime: time.Now().UTC(),
	}, nil
}

func (a *API) buildJourneyEnvironment(
	project Project,
	env string,
	buildImage string,
	fileSet map[string]struct{},
	transitions map[string]transitionArtifact,
) (projectJourneyEnv, error) {
	image, imageSource, err := a.resolveJourneyImage(project.ID, env, buildImage, fileSet)
	if err != nil {
		return projectJourneyEnv{}, err
	}
	state, deliveryType, deliveryPath, detail := journeyDeliveryForEnv(env, fileSet, transitions)

	return projectJourneyEnv{
		Name:         env,
		State:        state,
		Image:        image,
		ImageSource:  imageSource,
		DeliveryType: deliveryType,
		DeliveryPath: deliveryPath,
		Detail:       detail,
	}, nil
}

func (a *API) resolveJourneyImage(
	projectID string,
	env string,
	buildImage string,
	fileSet map[string]struct{},
) (string, string, error) {
	overlayImagePath := fmt.Sprintf("repos/manifests/overlays/%s/image.txt", env)
	if hasPath(fileSet, overlayImagePath) {
		image, err := a.readArtifactTrimmed(projectID, overlayImagePath)
		if err != nil {
			return "", "", err
		}
		if image != "" {
			return image, "environment marker", nil
		}
	}

	deployDeploymentPath := fmt.Sprintf("deploy/%s/deployment.yaml", env)
	if hasPath(fileSet, deployDeploymentPath) {
		data, err := a.artifacts.ReadFile(projectID, deployDeploymentPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		image := parseDeploymentImage(data)
		if image != "" {
			return image, "deployment manifest", nil
		}
	}

	if env == defaultDeployEnvironment && buildImage != "" {
		return buildImage, "latest build", nil
	}
	return "", "", nil
}

func journeyDeliveryForEnv(
	env string,
	fileSet map[string]struct{},
	transitions map[string]transitionArtifact,
) (string, string, string, string) {
	deployRenderedPath := fmt.Sprintf("deploy/%s/rendered.yaml", env)
	if hasPath(fileSet, deployRenderedPath) {
		return journeyEnvStateLive, "deploy", deployRenderedPath, "Deployment manifest is rendered for this environment."
	}

	if edge, ok := transitions[env]; ok {
		detail := fmt.Sprintf("Promoted from %s.", edge.from)
		if edge.action == "release" {
			detail = fmt.Sprintf("Released from %s.", edge.from)
		}
		return journeyEnvStateLive, edge.action, edge.path, detail
	}

	return journeyEnvStatePending, "", "", "Not delivered yet."
}

func hasPath(fileSet map[string]struct{}, path string) bool {
	_, ok := fileSet[path]
	return ok
}

func (a *API) readArtifactTrimmed(projectID, path string) (string, error) {
	data, err := a.artifacts.ReadFile(projectID, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func (a *API) readRecentOp(ctx context.Context, opID string) (Operation, bool, error) {
	opID = strings.TrimSpace(opID)
	if opID == "" {
		return Operation{}, false, nil
	}
	op, err := a.store.GetOp(ctx, opID)
	if err == nil {
		return op, true, nil
	}
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return Operation{}, false, nil
	}
	return Operation{}, false, err
}

func collectTransitionArtifacts(files []string) map[string]transitionArtifact {
	out := map[string]transitionArtifact{}
	for _, path := range files {
		action, from, to, ok := parseTransitionPath(path)
		if !ok {
			continue
		}
		out[to] = transitionArtifact{
			action: action,
			from:   from,
			to:     to,
			path:   path,
		}
	}
	return out
}

func parseTransitionPath(path string) (string, string, string, bool) {
	if strings.HasPrefix(path, "promotions/") {
		return parseTransitionPathFromRoot(path, "promotions/", "promote")
	}
	if strings.HasPrefix(path, "releases/") {
		return parseTransitionPathFromRoot(path, "releases/", "release")
	}
	return "", "", "", false
}

func parseTransitionPathFromRoot(path, root, action string) (string, string, string, bool) {
	rest := strings.TrimPrefix(path, root)
	parts := strings.SplitN(rest, "/", journeyPathPartsExpected)
	if len(parts) != journeyPathPartsExpected || parts[1] != "rendered.yaml" {
		return "", "", "", false
	}
	edge := strings.SplitN(parts[0], "-to-", journeyPathPartsExpected)
	if len(edge) != journeyPathPartsExpected {
		return "", "", "", false
	}
	from := normalizeEnvironmentName(edge[0])
	to := normalizeEnvironmentName(edge[1])
	if from == "" || to == "" {
		return "", "", "", false
	}
	return action, from, to, true
}

func parseDeploymentImage(data []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(trimmed, "image:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "image:"))
		if value != "" {
			return value
		}
	}
	return ""
}

func journeyEnvironmentOrder(spec ProjectSpec) []string {
	spec = normalizeProjectSpec(spec)
	envs := make([]string, 0, len(spec.Environments)+1)
	seen := map[string]struct{}{}

	envs = append(envs, defaultDeployEnvironment)
	seen[defaultDeployEnvironment] = struct{}{}

	for env := range spec.Environments {
		name := normalizeEnvironmentName(env)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		envs = append(envs, name)
	}

	sort.Slice(envs, func(i, j int) bool {
		return compareJourneyEnvironment(envs[i], envs[j])
	})
	return envs
}

func compareJourneyEnvironment(left, right string) bool {
	lRank := journeyEnvironmentRank(left)
	rRank := journeyEnvironmentRank(right)
	if lRank != rRank {
		return lRank < rRank
	}
	return left < right
}

func journeyEnvironmentRank(env string) int {
	switch normalizeEnvironmentName(env) {
	case defaultDeployEnvironment:
		return journeyRankDev
	case "staging":
		return journeyRankStaging
	case "prod", "production":
		return journeyRankProd
	default:
		return journeyRankOther
	}
}

func summarizeArtifacts(files []string) projectJourneyArtifactStat {
	stats := projectJourneyArtifactStat{
		Total:        len(files),
		Build:        0,
		Deploy:       0,
		Promotion:    0,
		Release:      0,
		Repository:   0,
		Registration: 0,
		Other:        0,
	}
	for _, file := range files {
		switch {
		case strings.HasPrefix(file, "build/"):
			stats.Build++
		case strings.HasPrefix(file, "deploy/"):
			stats.Deploy++
		case strings.HasPrefix(file, "promotions/"):
			stats.Promotion++
		case strings.HasPrefix(file, "releases/"):
			stats.Release++
		case strings.HasPrefix(file, "repos/"):
			stats.Repository++
		case strings.HasPrefix(file, "registration/"):
			stats.Registration++
		default:
			stats.Other++
		}
	}
	return stats
}

func buildJourneyMilestones(
	project Project,
	buildImage string,
	envs []projectJourneyEnv,
) []projectJourneyMilestone {
	milestones := []projectJourneyMilestone{
		{
			ID:     "registered",
			Title:  "App created",
			Status: journeyStatusComplete,
			Detail: fmt.Sprintf("App %q is available in your workspace.", project.Spec.Name),
		},
	}

	buildStatus := journeyStatusPending
	buildDetail := "No build image yet."
	switch {
	case buildImage != "":
		buildStatus = journeyStatusComplete
		buildDetail = fmt.Sprintf("Latest build image: %s.", buildImage)
	case project.Status.Phase == journeyPhaseReconciling &&
		(project.Status.LastOpKind == string(OpCreate) ||
			project.Status.LastOpKind == string(OpUpdate) ||
			project.Status.LastOpKind == string(OpCI)):
		buildStatus = journeyStatusInProgress
		buildDetail = "Build is currently in progress."
	case project.Status.Phase == projectPhaseError:
		buildStatus = journeyStatusFailed
		buildDetail = firstNonEmpty(project.Status.Message, "Build stage needs attention.")
	}
	milestones = append(milestones, projectJourneyMilestone{
		ID:     "build",
		Title:  "Build available",
		Status: buildStatus,
		Detail: buildDetail,
	})

	previousLive := true
	for _, env := range envs {
		live := env.State == journeyEnvStateLive
		status := journeyStatusPending
		detail := env.Detail

		switch {
		case live:
			status = journeyStatusComplete
		case !previousLive:
			status = journeyStatusBlocked
			detail = "Waiting for upstream environment delivery first."
		case project.Status.Phase == journeyPhaseReconciling &&
			isInProgressDelivery(project.Status.LastOpKind, env.Name):
			status = journeyStatusInProgress
			detail = "Delivery for this environment is in progress."
		case project.Status.Phase == projectPhaseError:
			status = journeyStatusFailed
			detail = firstNonEmpty(project.Status.Message, "Delivery failed for this environment.")
		}

		milestones = append(milestones, projectJourneyMilestone{
			ID:     fmt.Sprintf("env-%s", env.Name),
			Title:  fmt.Sprintf("%s live", strings.ToUpper(env.Name)),
			Status: status,
			Detail: detail,
		})
		previousLive = previousLive && live
	}

	return milestones
}

func isInProgressDelivery(lastOpKind string, env string) bool {
	kind := strings.TrimSpace(lastOpKind)
	if env == defaultDeployEnvironment {
		return kind == string(OpDeploy)
	}
	if isProductionEnvironment(env) {
		return kind == string(OpRelease)
	}
	return kind == string(OpPromote)
}

func recommendJourneyAction(
	project Project,
	buildImage string,
	envs []projectJourneyEnv,
) projectJourneyNextAction {
	if project.Status.Phase == projectPhaseError {
		return newJourneyNextAction(
			"investigate",
			"Review failing activity",
			firstNonEmpty(project.Status.Message, "Open the latest activity details, then retry the step."),
			"",
			"",
			"",
		)
	}
	if buildImage == "" {
		return newJourneyNextAction(
			"build",
			"Run a source build",
			"Trigger a source change build so delivery has an image to ship.",
			"",
			"",
			"",
		)
	}
	envIndex := map[string]int{}
	for idx, env := range envs {
		envIndex[env.Name] = idx
	}
	devIndex, hasDev := envIndex[defaultDeployEnvironment]
	if !hasDev || envs[devIndex].State != journeyEnvStateLive {
		return newJourneyNextAction(
			"deploy_dev",
			"Deploy to dev",
			"Ship the latest build to the dev environment.",
			defaultDeployEnvironment,
			"",
			"",
		)
	}

	for idx := 1; idx < len(envs); idx++ {
		target := envs[idx]
		if target.State == journeyEnvStateLive {
			continue
		}
		source := envs[idx-1]
		if source.State != journeyEnvStateLive {
			return newJourneyNextAction(
				"none",
				"Wait for upstream delivery",
				fmt.Sprintf("Deliver %s before moving to %s.", source.Name, target.Name),
				"",
				"",
				"",
			)
		}
		if isProductionEnvironment(target.Name) {
			return newJourneyNextAction(
				"release",
				"Release to production",
				fmt.Sprintf("Promote verified image from %s to %s.", source.Name, target.Name),
				"",
				source.Name,
				target.Name,
			)
		}
		return newJourneyNextAction(
			"promote",
			fmt.Sprintf("Promote to %s", target.Name),
			fmt.Sprintf("Move tested image from %s to %s.", source.Name, target.Name),
			"",
			source.Name,
			target.Name,
		)
	}

	return newJourneyNextAction(
		"none",
		"Journey is up to date",
		"All configured environments have delivery evidence.",
		"",
		"",
		"",
	)
}

func newJourneyNextAction(
	kind string,
	label string,
	detail string,
	environment string,
	fromEnv string,
	toEnv string,
) projectJourneyNextAction {
	return projectJourneyNextAction{
		Kind:        kind,
		Label:       label,
		Detail:      detail,
		Environment: environment,
		FromEnv:     fromEnv,
		ToEnv:       toEnv,
	}
}

func describeJourneySummary(project Project, buildImage string, envs []projectJourneyEnv) string {
	delivered := 0
	for _, env := range envs {
		if env.State == journeyEnvStateLive {
			delivered++
		}
	}
	switch {
	case project.Status.Phase == projectPhaseError:
		return firstNonEmpty(project.Status.Message, "Delivery needs attention.")
	case buildImage == "":
		return "App is created. Build output is still pending."
	case delivered == 0:
		return "Build is available. No environments have delivery evidence yet."
	case delivered < len(envs):
		return fmt.Sprintf("Delivery is underway: %d of %d environments are live.", delivered, len(envs))
	default:
		return "All configured environments have delivery evidence."
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
