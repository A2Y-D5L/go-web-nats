package platform

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"gopkg.in/yaml.v3"
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

		project, op, err := a.createProjectFromSpec(r.Context(), spec)
		if err != nil {
			if writeAsyncOpError(w, err) {
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{
			"accepted": true,
			"project":  project,
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
		switch parts[1] {
		case "artifacts":
			a.handleProjectArtifacts(w, r)
		case "ops":
			a.handleProjectOps(w, r)
		case "releases":
			a.handleProjectReleases(w, r)
		case "overview":
			a.handleProjectOverview(w, r)
		case "journey":
			a.handleProjectJourney(w, r)
		default:
			http.NotFound(w, r)
		}
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
		if writeAsyncOpError(w, err) {
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
		if writeAsyncOpError(w, err) {
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

func (a *API) handleProjectReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.store == nil {
		http.Error(w, "release data unavailable", http.StatusInternalServerError)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/projects/") {
		http.NotFound(w, r)
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) < projectRelPathPartsMin || parts[1] != "releases" {
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

	if len(parts) == projectRelPathPartsMin {
		a.handleProjectReleaseList(w, r, project)
		return
	}
	if len(parts) == projectRelPathPartsMin+1 {
		if strings.EqualFold(strings.TrimSpace(parts[2]), "compare") {
			a.handleProjectReleaseCompare(w, r, project.ID)
			return
		}
		a.handleProjectReleaseDetail(w, r, projectID, strings.TrimSpace(parts[2]))
		return
	}
	http.NotFound(w, r)
}

func (a *API) handleProjectReleaseList(w http.ResponseWriter, r *http.Request, project Project) {
	environmentRaw := normalizeEnvironmentName(r.URL.Query().Get("environment"))
	if environmentRaw == "" {
		http.Error(w, "environment query parameter required", http.StatusBadRequest)
		return
	}
	environment, ok := resolveProjectEnvironmentName(project.Spec, environmentRaw)
	if !ok {
		http.Error(
			w,
			fmt.Sprintf("environment %q is not defined for project", environmentRaw),
			http.StatusBadRequest,
		)
		return
	}

	limit, err := parseProjectReleaseLimitParam(r.URL.Query().Get("limit"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	page, err := a.store.listProjectReleases(
		r.Context(),
		project.ID,
		environment,
		projectReleaseListQuery{
			Limit:  limit,
			Cursor: r.URL.Query().Get("cursor"),
		},
	)
	if err != nil {
		http.Error(w, "failed to list releases", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, projectReleaseListResponse(page))
}

func (a *API) handleProjectReleaseDetail(
	w http.ResponseWriter,
	r *http.Request,
	projectID string,
	releaseID string,
) {
	if releaseID == "" {
		http.Error(w, "bad release id", http.StatusBadRequest)
		return
	}
	release, err := a.store.GetRelease(r.Context(), releaseID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to read release", http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(release.ProjectID) != strings.TrimSpace(projectID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, release)
}

func (a *API) handleProjectReleaseCompare(w http.ResponseWriter, r *http.Request, projectID string) {
	fromID := strings.TrimSpace(r.URL.Query().Get("from"))
	toID := strings.TrimSpace(r.URL.Query().Get("to"))
	if fromID == "" || toID == "" {
		http.Error(w, "from and to query parameters are required", http.StatusBadRequest)
		return
	}

	fromRelease, err := a.store.GetRelease(r.Context(), fromID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to read release", http.StatusInternalServerError)
		return
	}
	toRelease, err := a.store.GetRelease(r.Context(), toID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to read release", http.StatusInternalServerError)
		return
	}
	if strings.TrimSpace(fromRelease.ProjectID) != strings.TrimSpace(projectID) ||
		strings.TrimSpace(toRelease.ProjectID) != strings.TrimSpace(projectID) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	response, err := a.buildReleaseCompareResponseFromRecords(r.Context(), projectID, fromRelease, toRelease)
	if err != nil {
		http.Error(w, "failed to compare releases", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func parseProjectReleaseLimitParam(raw string) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return projectReleaseDefaultLimit, nil
	}
	parsed, err := strconv.Atoi(trimmed)
	if err != nil || parsed <= 0 {
		return 0, errors.New("bad limit")
	}
	return normalizeProjectReleaseLimit(parsed), nil
}

func (a *API) buildReleaseCompareResponseFromRecords(
	ctx context.Context,
	projectID string,
	fromRelease ReleaseRecord,
	toRelease ReleaseRecord,
) (ReleaseCompareResponse, error) {
	projectID = strings.TrimSpace(projectID)
	fromRelease = normalizeReleaseRecord(fromRelease)
	toRelease = normalizeReleaseRecord(toRelease)

	imageFrom := strings.TrimSpace(fromRelease.Image)
	imageTo := strings.TrimSpace(toRelease.Image)
	imageDelta := ReleaseCompareDelta{
		Changed: imageFrom != imageTo,
		From:    imageFrom,
		To:      imageTo,
		Added:   nil,
		Removed: nil,
		Updated: nil,
	}

	fromVars, err := a.readReleaseConfigVars(ctx, projectID, fromRelease)
	if err != nil {
		return ReleaseCompareResponse{}, err
	}
	toVars, err := a.readReleaseConfigVars(ctx, projectID, toRelease)
	if err != nil {
		return ReleaseCompareResponse{}, err
	}
	addedVars, removedVars, updatedVars := diffStringMap(fromVars, toVars)
	configDelta := ReleaseCompareDelta{
		Changed: len(addedVars) > 0 || len(removedVars) > 0 || len(updatedVars) > 0,
		From:    stableConfigFingerprint(fromVars),
		To:      stableConfigFingerprint(toVars),
		Added:   addedVars,
		Removed: removedVars,
		Updated: updatedVars,
	}

	fromRendered, fromRenderedHash, err := a.readCanonicalRenderedSnapshot(ctx, projectID, fromRelease)
	if err != nil {
		return ReleaseCompareResponse{}, err
	}
	toRendered, toRenderedHash, err := a.readCanonicalRenderedSnapshot(ctx, projectID, toRelease)
	if err != nil {
		return ReleaseCompareResponse{}, err
	}
	renderedDelta := ReleaseCompareDelta{
		Changed: fromRendered != toRendered,
		From:    fromRenderedHash,
		To:      toRenderedHash,
		Added:   nil,
		Removed: nil,
		Updated: nil,
	}

	fromCopy := fromRelease
	toCopy := toRelease
	return ReleaseCompareResponse{
		FromID:      strings.TrimSpace(fromRelease.ID),
		ToID:        strings.TrimSpace(toRelease.ID),
		FromRelease: &fromCopy,
		ToRelease:   &toCopy,
		Summary: fmt.Sprintf(
			"Image changed: %t. Config vars: +%d -%d ~%d. Rendered manifest changed (noise-filtered): %t.",
			imageDelta.Changed,
			len(configDelta.Added),
			len(configDelta.Removed),
			len(configDelta.Updated),
			renderedDelta.Changed,
		),
		ImageDelta:    imageDelta,
		ConfigDelta:   configDelta,
		RenderedDelta: renderedDelta,
	}, nil
}

func (a *API) readReleaseConfigVars(
	ctx context.Context,
	projectID string,
	release ReleaseRecord,
) (map[string]string, error) {
	raw, err := a.readReleaseDeploymentSnapshot(ctx, projectID, release)
	if err != nil || len(raw) == 0 {
		return map[string]string{}, err
	}
	return parseDeploymentEnvVars(raw), nil
}

func (a *API) readReleaseDeploymentSnapshot(
	_ context.Context,
	projectID string,
	release ReleaseRecord,
) ([]byte, error) {
	if a == nil || a.artifacts == nil {
		return nil, nil
	}
	paths := []string{}
	if path := strings.Trim(strings.TrimSpace(release.ConfigPath), "/"); path != "" {
		paths = append(paths, path)
	}
	if renderedPath := strings.Trim(strings.TrimSpace(release.RenderedPath), "/"); renderedPath != "" {
		if base, ok := strings.CutSuffix(renderedPath, "/rendered.yaml"); ok {
			paths = append(paths, base+"/deployment.yaml")
		}
		paths = append(paths, renderedPath)
	}
	for _, path := range paths {
		raw, err := a.artifacts.ReadFile(projectID, path)
		if err == nil {
			return raw, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to read release artifact %q: %w", path, err)
		}
	}
	return nil, nil
}

func parseDeploymentEnvVars(raw []byte) map[string]string {
	vars := map[string]string{}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		doc, done := decodeDeploymentManifestDocument(decoder)
		if done {
			break
		}
		if len(doc) == 0 {
			continue
		}
		collectDeploymentEnvVars(vars, doc)
	}
	return vars
}

func decodeDeploymentManifestDocument(decoder *yaml.Decoder) (map[string]any, bool) {
	var doc map[string]any
	err := decoder.Decode(&doc)
	if errors.Is(err, io.EOF) {
		return nil, true
	}
	if err != nil || len(doc) == 0 {
		return nil, false
	}
	return doc, false
}

func collectDeploymentEnvVars(vars map[string]string, doc map[string]any) {
	if !isDeploymentManifestKind(doc) {
		return
	}
	for _, containerRaw := range deploymentContainers(doc) {
		collectContainerEnvVars(vars, valueAsMap(containerRaw))
	}
}

func isDeploymentManifestKind(doc map[string]any) bool {
	return strings.EqualFold(
		strings.TrimSpace(valueAsString(doc["kind"])),
		"Deployment",
	)
}

func deploymentContainers(doc map[string]any) []any {
	spec := valueAsMap(doc["spec"])
	template := valueAsMap(spec["template"])
	templateSpec := valueAsMap(valueAsMap(template["spec"]))
	return valueAsSlice(templateSpec["containers"])
}

func collectContainerEnvVars(vars map[string]string, container map[string]any) {
	for _, envRaw := range valueAsSlice(container["env"]) {
		envEntry := valueAsMap(envRaw)
		name := strings.TrimSpace(valueAsString(envEntry["name"]))
		if name == "" {
			continue
		}
		if value, ok := envEntry["value"]; ok {
			vars[name] = strings.TrimSpace(valueAsString(value))
			continue
		}
		if _, ok := envEntry["valueFrom"]; ok {
			vars[name] = "<valueFrom>"
		}
	}
}

func valueAsMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	default:
		return map[string]any{}
	}
}

func valueAsSlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	default:
		return nil
	}
}

func valueAsString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(value)
	}
}

func diffStringMap(from map[string]string, to map[string]string) ([]string, []string, []string) {
	added := []string{}
	removed := []string{}
	updated := []string{}

	for key, toValue := range to {
		fromValue, exists := from[key]
		if !exists {
			added = append(added, key)
			continue
		}
		if fromValue != toValue {
			updated = append(updated, key)
		}
	}
	for key := range from {
		if _, exists := to[key]; !exists {
			removed = append(removed, key)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(updated)
	return added, removed, updated
}

func stableConfigFingerprint(vars map[string]string) string {
	if len(vars) == 0 {
		return ""
	}
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+vars[key])
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func (a *API) readCanonicalRenderedSnapshot(
	_ context.Context,
	projectID string,
	release ReleaseRecord,
) (string, string, error) {
	if a == nil || a.artifacts == nil {
		return "", "", nil
	}
	renderedPath := strings.Trim(strings.TrimSpace(release.RenderedPath), "/")
	if renderedPath == "" {
		return "", "", nil
	}
	raw, err := a.artifacts.ReadFile(projectID, renderedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("failed to read rendered snapshot %q: %w", renderedPath, err)
	}
	canonical := canonicalManifestForCompare(raw)
	if canonical == "" {
		return "", "", nil
	}
	sum := sha256.Sum256([]byte(canonical))
	return canonical, hex.EncodeToString(sum[:]), nil
}

func canonicalManifestForCompare(raw []byte) string {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	canonicalDocs := []string{}
	for {
		var doc any
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return canonicalManifestLinesFallback(raw)
		}
		if doc == nil {
			continue
		}
		sanitized := sanitizeManifestCompareValue(doc, "")
		encoded, marshalErr := json.Marshal(sanitized)
		if marshalErr != nil {
			return canonicalManifestLinesFallback(raw)
		}
		canonicalDocs = append(canonicalDocs, string(encoded))
	}
	if len(canonicalDocs) == 0 {
		return canonicalManifestLinesFallback(raw)
	}
	return strings.Join(canonicalDocs, "\n")
}

func sanitizeManifestCompareValue(value any, parentKey string) any {
	switch typed := value.(type) {
	case map[string]any:
		return sanitizeManifestCompareMap(typed, parentKey)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeManifestCompareValue(item, parentKey))
		}
		return out
	default:
		return typed
	}
}

func sanitizeManifestCompareMap(in map[string]any, parentKey string) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		trimmedKey := strings.TrimSpace(key)
		if shouldDropManifestCompareField(parentKey, trimmedKey) {
			continue
		}
		if parentKey == "annotations" && shouldDropManifestCompareAnnotation(trimmedKey) {
			continue
		}
		out[trimmedKey] = sanitizeManifestCompareValue(value, trimmedKey)
	}
	return out
}

func shouldDropManifestCompareField(parentKey string, key string) bool {
	if parentKey != "metadata" {
		return false
	}
	switch key {
	case "creationTimestamp", "resourceVersion", "uid", "managedFields", "generation":
		return true
	default:
		return false
	}
}

func shouldDropManifestCompareAnnotation(key string) bool {
	switch key {
	case "kubectl.kubernetes.io/last-applied-configuration", "deployment.kubernetes.io/revision":
		return true
	default:
		return false
	}
}

func canonicalManifestLinesFallback(raw []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	lines := []string{}
	for scanner.Scan() {
		trimmed := strings.TrimSpace(scanner.Text())
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "creationTimestamp:") ||
			strings.HasPrefix(trimmed, "resourceVersion:") ||
			strings.HasPrefix(trimmed, "uid:") ||
			strings.HasPrefix(trimmed, "managedFields:") ||
			strings.Contains(trimmed, "kubectl.kubernetes.io/last-applied-configuration") ||
			strings.Contains(trimmed, "deployment.kubernetes.io/revision") {
			continue
		}
		lines = append(lines, trimmed)
	}
	return strings.Join(lines, "\n")
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

type projectOverview struct {
	Summary      string               `json:"summary"`
	Environments []projectOverviewEnv `json:"environments"`
}

type projectOverviewEnv struct {
	Name             string     `json:"name"`
	HealthStatus     string     `json:"health_status"`
	DeliveryState    string     `json:"delivery_state"`
	RunningImage     string     `json:"running_image,omitempty"`
	DeliveryType     string     `json:"delivery_type"`
	DeliveryPath     string     `json:"delivery_path,omitempty"`
	ConfigReadiness  string     `json:"config_readiness"`
	SecretsReadiness string     `json:"secrets_readiness"`
	LastDeliveryAt   *time.Time `json:"last_delivery_at,omitempty"`
}

type projectReleaseListResponse struct {
	Items      []ReleaseRecord `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
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

	overviewHealthHealthy      = "healthy"
	overviewHealthDegraded     = "degraded"
	overviewHealthFailing      = "failing"
	overviewHealthUnknown      = "unknown"
	overviewDeliveryTypeNone   = "none"
	overviewConfigReadinessOK  = "ok"
	overviewConfigReadinessUnk = "unknown"
	overviewSecretsUnsupported = "unsupported"
)

func (a *API) handleProjectOverview(w http.ResponseWriter, r *http.Request) {
	a.handleProjectReadModel(
		w,
		r,
		"overview",
		"overview data unavailable",
		"failed to build project overview",
		"overview",
		func(ctx context.Context, project Project, files []string) (any, error) {
			return a.buildProjectOverview(ctx, project, files)
		},
	)
}

func (a *API) handleProjectJourney(w http.ResponseWriter, r *http.Request) {
	a.handleProjectReadModel(
		w,
		r,
		"journey",
		"journey data unavailable",
		"failed to build project journey",
		"journey",
		func(ctx context.Context, project Project, files []string) (any, error) {
			return a.buildProjectJourney(ctx, project, files)
		},
	)
}

func (a *API) handleProjectReadModel(
	w http.ResponseWriter,
	r *http.Request,
	subresource string,
	unavailableMessage string,
	buildFailureMessage string,
	responseKey string,
	build func(context.Context, Project, []string) (any, error),
) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.store == nil || a.artifacts == nil {
		http.Error(w, unavailableMessage, http.StatusInternalServerError)
		return
	}

	projectID, ok := projectIDFromSubresourcePath(w, r, subresource)
	if !ok {
		return
	}

	project, found := a.getProjectOrWriteError(w, r, projectID)
	if !found {
		return
	}
	files, err := a.artifacts.ListFiles(projectID)
	if err != nil {
		http.Error(w, "failed to list artifacts", http.StatusInternalServerError)
		return
	}

	readModel, err := build(r.Context(), project, files)
	if err != nil {
		http.Error(w, buildFailureMessage, http.StatusInternalServerError)
		return
	}

	payload := map[string]any{
		"project": project,
	}
	payload[responseKey] = readModel
	writeJSON(w, http.StatusOK, payload)
}

func projectIDFromSubresourcePath(w http.ResponseWriter, r *http.Request, subresource string) (string, bool) {
	if !strings.HasPrefix(r.URL.Path, "/api/projects/") {
		http.NotFound(w, r)
		return "", false
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/projects/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != journeyPathPartsExpected || parts[1] != subresource {
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

func (a *API) buildProjectOverview(
	ctx context.Context,
	project Project,
	files []string,
) (projectOverview, error) {
	journey, err := a.buildProjectJourney(ctx, project, files)
	if err != nil {
		return projectOverview{}, err
	}

	envs := make([]projectOverviewEnv, 0, len(journey.Environments))
	for _, env := range journey.Environments {
		envs = append(envs, buildOverviewEnvironment(project, env, journey.RecentOp))
	}

	return projectOverview{
		Summary:      journey.Summary,
		Environments: envs,
	}, nil
}

func buildOverviewEnvironment(
	project Project,
	journeyEnv projectJourneyEnv,
	recentOp *Operation,
) projectOverviewEnv {
	deliveryType := strings.TrimSpace(journeyEnv.DeliveryType)
	if deliveryType == "" {
		deliveryType = overviewDeliveryTypeNone
	}

	configReadiness := overviewConfigReadinessUnk
	if journeyEnv.State == journeyEnvStateLive || strings.TrimSpace(journeyEnv.DeliveryPath) != "" {
		configReadiness = overviewConfigReadinessOK
	}

	return projectOverviewEnv{
		Name:             journeyEnv.Name,
		HealthStatus:     overviewHealthStatus(project, journeyEnv),
		DeliveryState:    journeyEnv.State,
		RunningImage:     journeyEnv.Image,
		DeliveryType:     deliveryType,
		DeliveryPath:     journeyEnv.DeliveryPath,
		ConfigReadiness:  configReadiness,
		SecretsReadiness: overviewSecretsUnsupported,
		LastDeliveryAt:   overviewLastDeliveryAt(journeyEnv.Name, recentOp),
	}
}

func overviewHealthStatus(project Project, env projectJourneyEnv) string {
	switch {
	case project.Status.Phase == projectPhaseError:
		return overviewHealthFailing
	case project.Status.Phase == journeyPhaseReconciling && env.State != journeyEnvStateLive:
		return overviewHealthDegraded
	case env.State == journeyEnvStateLive:
		return overviewHealthHealthy
	default:
		return overviewHealthUnknown
	}
}

func overviewLastDeliveryAt(env string, recentOp *Operation) *time.Time {
	if recentOp == nil {
		return nil
	}
	if recentOp.Status != opStatusDone {
		return nil
	}
	if !isRecentDeliveryForEnvironment(*recentOp, env) {
		return nil
	}

	at := recentOp.Finished
	if at.IsZero() {
		at = recentOp.Requested
	}
	if at.IsZero() {
		return nil
	}
	at = at.UTC()
	return &at
}

func isRecentDeliveryForEnvironment(op Operation, env string) bool {
	target := normalizeEnvironmentName(env)
	switch op.Kind {
	case OpDeploy:
		delivered := normalizeEnvironmentName(op.Delivery.Environment)
		if delivered == "" {
			delivered = defaultDeployEnvironment
		}
		return delivered == target
	case OpPromote, OpRelease:
		delivered := normalizeEnvironmentName(op.Delivery.ToEnv)
		return delivered != "" && delivered == target
	case OpRollback:
		delivered := normalizeEnvironmentName(op.Delivery.Environment)
		if delivered == "" {
			delivered = normalizeEnvironmentName(op.Delivery.ToEnv)
		}
		return delivered != "" && delivered == target
	case OpCreate, OpUpdate, OpDelete, OpCI:
		return false
	default:
		return false
	}
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
