package platform

import (
	"context"
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

	previewGatePassed  = "passed"
	previewGateBlocked = "blocked"
	previewGateWarning = "warning"

	transitionBlockerActiveOperation = "active_operation"
	transitionBlockerInvalidMove     = "invalid_transition"
	transitionBlockerSourceImage     = "source_missing_image"
	transitionBlockerSourceDelivery  = "source_not_delivered"
	transitionBlockerTargetMissing   = "target_unavailable"

	transitionPreviewBlockerCapacity = 5
)

type transitionLifecycleContext struct {
	project Project
	spec    ProjectSpec
	fromEnv string
	toEnv   string
	stage   DeliveryStage
	kind    OperationKind
}

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

	op, err := a.enqueueOp(
		r.Context(),
		OpDeploy,
		project.ID,
		project.Spec,
		deployOpRunOptions(env),
	)
	if err != nil {
		if writeAsyncOpError(w, err) {
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	project, _ = a.store.GetProject(r.Context(), project.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"project":  project,
		"op":       op,
	})
}

func (a *API) handlePromotionPreviewEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var evt PromotionEvent
	if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	projectID := strings.TrimSpace(evt.ProjectID)
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}

	preview, err := a.runTransitionPreviewLifecycle(
		r,
		projectID,
		evt.FromEnv,
		evt.ToEnv,
	)
	if err != nil {
		writeTransitionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
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
	op, project, err := a.runTransitionLifecycle(
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
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"project":  project,
		"op":       op,
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

	op, project, err := a.runTransitionLifecycle(
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
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted": true,
		"project":  project,
		"op":       op,
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
	if writeAsyncOpError(w, err) {
		return
	}
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
) (Operation, Project, error) {
	lifecycle, err := a.resolveTransitionLifecycleContext(
		r.Context(),
		projectID,
		fromEnvRaw,
		toEnvRaw,
		releaseOnly,
	)
	if err != nil {
		return Operation{}, Project{}, err
	}

	op, err := a.enqueueOp(
		r.Context(),
		lifecycle.kind,
		lifecycle.project.ID,
		lifecycle.spec,
		transitionOpRunOptions(lifecycle.fromEnv, lifecycle.toEnv, lifecycle.stage),
	)
	if err != nil {
		return Operation{}, Project{}, err
	}
	latestProject, readErr := a.store.GetProject(r.Context(), lifecycle.project.ID)
	if readErr == nil {
		lifecycle.project = latestProject
	}
	return op, lifecycle.project, nil
}

func (a *API) resolveTransitionLifecycleContext(
	ctx context.Context,
	projectID string,
	fromEnvRaw string,
	toEnvRaw string,
	releaseOnly bool,
) (transitionLifecycleContext, error) {
	project, err := a.store.GetProject(ctx, projectID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return transitionLifecycleContext{}, requestError(http.StatusNotFound, "not found")
		}
		return transitionLifecycleContext{}, fmt.Errorf("failed to read project: %w", err)
	}
	spec := normalizeProjectSpec(project.Spec)
	fromEnv, toEnv, stage, kind, err := resolveTransitionRequest(
		spec,
		fromEnvRaw,
		toEnvRaw,
		releaseOnly,
	)
	if err != nil {
		return transitionLifecycleContext{}, err
	}
	return transitionLifecycleContext{
		project: project,
		spec:    spec,
		fromEnv: fromEnv,
		toEnv:   toEnv,
		stage:   stage,
		kind:    kind,
	}, nil
}

func resolveTransitionRequest(
	spec ProjectSpec,
	fromEnvRaw string,
	toEnvRaw string,
	releaseOnly bool,
) (string, string, DeliveryStage, OperationKind, error) {
	fromEnv := normalizeEnvironmentName(fromEnvRaw)
	toEnv := normalizeEnvironmentName(toEnvRaw)
	if fromEnv == "" || toEnv == "" {
		return "", "", "", "", requestError(
			http.StatusBadRequest,
			"from_env and to_env are required",
		)
	}
	if !isValidEnvironmentName(fromEnv) || !isValidEnvironmentName(toEnv) {
		return "", "", "", "", requestError(
			http.StatusBadRequest,
			"from_env and to_env must be valid environment names",
		)
	}

	spec = normalizeProjectSpec(spec)
	resolvedFromEnv, ok := resolveProjectEnvironmentName(spec, fromEnv)
	if !ok {
		return "", "", "", "", requestError(
			http.StatusBadRequest,
			fmt.Sprintf("from_env %q is not defined for project", fromEnv),
		)
	}
	resolvedToEnv, ok := resolveProjectEnvironmentName(spec, toEnv)
	if !ok {
		return "", "", "", "", requestError(
			http.StatusBadRequest,
			fmt.Sprintf("to_env %q is not defined for project", toEnv),
		)
	}
	if resolvedFromEnv == resolvedToEnv {
		return "", "", "", "", requestError(
			http.StatusBadRequest,
			"from_env and to_env must differ",
		)
	}

	stage := transitionDeliveryStage(resolvedToEnv)
	if releaseOnly && stage != DeliveryStageRelease {
		return "", "", "", "", requestError(
			http.StatusBadRequest,
			fmt.Sprintf(
				"release endpoint requires production target environment (got %q)",
				resolvedToEnv,
			),
		)
	}
	kind := transitionOperationKind(stage)
	return resolvedFromEnv, resolvedToEnv, stage, kind, nil
}

func (a *API) runTransitionPreviewLifecycle(
	r *http.Request,
	projectID string,
	fromEnvRaw string,
	toEnvRaw string,
) (PromotionPreviewResponse, error) {
	project, err := a.store.GetProject(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return PromotionPreviewResponse{}, requestError(http.StatusNotFound, "not found")
		}
		return PromotionPreviewResponse{}, fmt.Errorf("failed to read project: %w", err)
	}
	spec := normalizeProjectSpec(project.Spec)

	preview := PromotionPreviewResponse{
		Action:        transitionActionFromTarget(toEnvRaw),
		SourceRelease: nil,
		TargetRelease: nil,
		ChangeSummary: "",
		Gates:         []TransitionPreviewGate{},
		Blockers:      []TransitionPreviewBlocker{},
		RolloutPlan:   transitionRolloutPlan(),
	}

	blockersByCode := map[string]TransitionPreviewBlocker{}
	blockerOrder := make([]string, 0, transitionPreviewBlockerCapacity)

	kind := transitionOperationKind(transitionDeliveryStage(normalizeEnvironmentName(toEnvRaw)))
	if err = a.addActiveOperationPreviewBlocker(
		r.Context(),
		project.ID,
		kind,
		blockersByCode,
		&blockerOrder,
	); err != nil {
		return PromotionPreviewResponse{}, err
	}
	addTargetUnavailablePreviewBlocker(spec, toEnvRaw, blockersByCode, &blockerOrder)

	resolvedFromEnv, resolvedToEnv, stage, resolvedKind, transitionErr := resolveTransitionRequest(
		spec,
		fromEnvRaw,
		toEnvRaw,
		false,
	)
	details := transitionPreviewDetails{
		resolvedFromEnv:    resolvedFromEnv,
		resolvedToEnv:      resolvedToEnv,
		kind:               kind,
		sourceImage:        "",
		targetImage:        "",
		targetReleaseFound: false,
		sourceRelease:      nil,
		targetRelease:      nil,
	}
	if transitionErr != nil {
		addTransitionPreviewBlocker(blockersByCode, &blockerOrder, TransitionPreviewBlocker{
			Code:       transitionBlockerInvalidMove,
			Message:    "Transition request is invalid.",
			Why:        transitionErr.Error(),
			NextAction: "Provide valid, different source/target environments and retry preview.",
		})
	} else {
		preview.Action = transitionActionFromStage(stage)
		details, err = a.resolveTransitionPreviewDetails(
			r.Context(),
			project,
			spec,
			resolvedFromEnv,
			resolvedToEnv,
			resolvedKind,
			blockersByCode,
			&blockerOrder,
		)
		if err != nil {
			return PromotionPreviewResponse{}, err
		}
		preview.SourceRelease = details.sourceRelease
		preview.TargetRelease = details.targetRelease
	}

	preview.Blockers = orderedTransitionPreviewBlockers(blockersByCode, blockerOrder)
	preview.ChangeSummary = transitionPreviewChangeSummary(preview, details)
	preview.Gates = transitionPreviewGates(blockersByCode, details.targetReleaseFound)
	return preview, nil
}

type transitionPreviewDetails struct {
	resolvedFromEnv    string
	resolvedToEnv      string
	kind               OperationKind
	sourceImage        string
	targetImage        string
	targetReleaseFound bool
	sourceRelease      *TransitionPreviewRelease
	targetRelease      *TransitionPreviewRelease
}

func (a *API) addActiveOperationPreviewBlocker(
	ctx context.Context,
	projectID string,
	kind OperationKind,
	blockersByCode map[string]TransitionPreviewBlocker,
	blockerOrder *[]string,
) error {
	conflictErr := a.projectOperationConflict(ctx, projectID, kind)
	if conflictErr == nil {
		return nil
	}
	var conflict projectOpConflictError
	if !errors.As(conflictErr, &conflict) {
		return conflictErr
	}
	addTransitionPreviewBlocker(blockersByCode, blockerOrder, TransitionPreviewBlocker{
		Code:       transitionBlockerActiveOperation,
		Message:    "Project has an active operation.",
		Why:        conflictErr.Error(),
		NextAction: "Wait for the active operation to reach done or error, then retry preview.",
	})
	return nil
}

func addTargetUnavailablePreviewBlocker(
	spec ProjectSpec,
	toEnvRaw string,
	blockersByCode map[string]TransitionPreviewBlocker,
	blockerOrder *[]string,
) {
	toEnv := normalizeEnvironmentName(toEnvRaw)
	if toEnv == "" || !isValidEnvironmentName(toEnv) || projectSupportsEnvironment(spec, toEnv) {
		return
	}
	addTransitionPreviewBlocker(blockersByCode, blockerOrder, TransitionPreviewBlocker{
		Code:       transitionBlockerTargetMissing,
		Message:    fmt.Sprintf("Target environment %q is unavailable for this project.", toEnv),
		Why:        "Promotion targets must map to a configured project environment.",
		NextAction: "Choose a configured target environment and retry preview.",
	})
}

func (a *API) resolveTransitionPreviewDetails(
	ctx context.Context,
	project Project,
	spec ProjectSpec,
	resolvedFromEnv string,
	resolvedToEnv string,
	kind OperationKind,
	blockersByCode map[string]TransitionPreviewBlocker,
	blockerOrder *[]string,
) (transitionPreviewDetails, error) {
	details := transitionPreviewDetails{
		resolvedFromEnv:    resolvedFromEnv,
		resolvedToEnv:      resolvedToEnv,
		kind:               kind,
		sourceImage:        "",
		targetImage:        "",
		targetReleaseFound: false,
		sourceRelease:      nil,
		targetRelease:      nil,
	}

	sourceRelease, found, err := a.store.getProjectCurrentRelease(ctx, project.ID, resolvedFromEnv)
	if err != nil {
		return details, fmt.Errorf("failed to read source release: %w", err)
	}
	if found {
		details.sourceRelease = transitionPreviewReleasePtr(sourceRelease)
	} else {
		addTransitionPreviewBlocker(blockersByCode, blockerOrder, TransitionPreviewBlocker{
			Code:       transitionBlockerSourceDelivery,
			Message:    fmt.Sprintf("Source environment %q has no delivered release.", resolvedFromEnv),
			Why:        "Promotions and releases require a delivered source to copy forward.",
			NextAction: fmt.Sprintf("Deliver or promote into %q first, then retry preview.", resolvedFromEnv),
		})
	}

	targetRelease, found, err := a.store.getProjectCurrentRelease(ctx, project.ID, resolvedToEnv)
	if err != nil {
		return details, fmt.Errorf("failed to read target release: %w", err)
	}
	if found {
		details.targetReleaseFound = true
		details.targetRelease = transitionPreviewReleasePtr(targetRelease)
	}

	imageByEnv, err := loadManifestImageTags(a.artifacts, project.ID, spec)
	if err != nil {
		return details, fmt.Errorf("failed to read manifest image tags: %w", err)
	}
	details.sourceImage, err = resolvePromotionSourceImage(a.artifacts, project.ID, resolvedFromEnv, imageByEnv)
	if err != nil {
		return details, fmt.Errorf("failed to resolve source image: %w", err)
	}

	renderedSourceImage, err := readRenderedEnvImageTag(a.artifacts, project.ID, resolvedFromEnv)
	if err != nil {
		return details, fmt.Errorf("failed to read source rendered image: %w", err)
	}
	if strings.TrimSpace(renderedSourceImage) == "" || strings.TrimSpace(details.sourceImage) == "" {
		addTransitionPreviewBlocker(blockersByCode, blockerOrder, TransitionPreviewBlocker{
			Code:       transitionBlockerSourceImage,
			Message:    fmt.Sprintf("Source environment %q has no rendered image.", resolvedFromEnv),
			Why:        "Transition rendering requires a concrete source image tag.",
			NextAction: fmt.Sprintf("Deliver %q first so rendered manifests include an image.", resolvedFromEnv),
		})
	}

	details.targetImage, err = readRenderedEnvImageTag(a.artifacts, project.ID, resolvedToEnv)
	if err != nil {
		return details, fmt.Errorf("failed to read target rendered image: %w", err)
	}
	return details, nil
}

func addTransitionPreviewBlocker(
	blockersByCode map[string]TransitionPreviewBlocker,
	blockerOrder *[]string,
	blocker TransitionPreviewBlocker,
) {
	if strings.TrimSpace(blocker.Code) == "" {
		return
	}
	if _, exists := blockersByCode[blocker.Code]; !exists {
		*blockerOrder = append(*blockerOrder, blocker.Code)
	}
	blockersByCode[blocker.Code] = blocker
}

func orderedTransitionPreviewBlockers(
	blockersByCode map[string]TransitionPreviewBlocker,
	blockerOrder []string,
) []TransitionPreviewBlocker {
	out := make([]TransitionPreviewBlocker, 0, len(blockerOrder))
	for _, code := range blockerOrder {
		out = append(out, blockersByCode[code])
	}
	return out
}

func transitionPreviewChangeSummary(
	preview PromotionPreviewResponse,
	details transitionPreviewDetails,
) string {
	if len(preview.Blockers) > 0 {
		return fmt.Sprintf(
			"Preview is blocked by %d blocker(s). Resolve blockers before confirming the transition.",
			len(preview.Blockers),
		)
	}

	targetDisplay := strings.TrimSpace(details.targetImage)
	if targetDisplay == "" && preview.TargetRelease != nil {
		targetDisplay = strings.TrimSpace(preview.TargetRelease.Image)
	}
	if targetDisplay == "" {
		targetDisplay = "not currently deployed"
	}
	verb := "Promote"
	if details.kind == OpRelease {
		verb = "Release"
	}
	return fmt.Sprintf(
		"%s image %q from %s to %s (target currently %q).",
		verb,
		strings.TrimSpace(details.sourceImage),
		details.resolvedFromEnv,
		details.resolvedToEnv,
		targetDisplay,
	)
}

func transitionPreviewGates(
	blockersByCode map[string]TransitionPreviewBlocker,
	targetReleaseFound bool,
) []TransitionPreviewGate {
	targetGateStatus := previewGatePassed
	targetGateDetail := "Target environment is available for this project."
	if hasTransitionPreviewBlocker(blockersByCode, transitionBlockerTargetMissing) {
		targetGateStatus = previewGateBlocked
		targetGateDetail = "Target environment is not configured for this project."
	} else if !targetReleaseFound {
		targetGateStatus = previewGateWarning
		targetGateDetail = "Target environment has no current release record yet."
	}

	return []TransitionPreviewGate{
		{
			Code:   transitionBlockerActiveOperation,
			Title:  "No active operation in progress",
			Status: previewGateStatus(hasTransitionPreviewBlocker(blockersByCode, transitionBlockerActiveOperation)),
			Detail: "Transitions should start only when the project has no queued or running operation.",
		},
		{
			Code:   transitionBlockerInvalidMove,
			Title:  "Transition request is valid",
			Status: previewGateStatus(hasTransitionPreviewBlocker(blockersByCode, transitionBlockerInvalidMove)),
			Detail: "Source and target environments must both be defined and must differ.",
		},
		{
			Code:   transitionBlockerSourceDelivery,
			Title:  "Source environment has delivered release evidence",
			Status: previewGateStatus(hasTransitionPreviewBlocker(blockersByCode, transitionBlockerSourceDelivery)),
			Detail: "Source environment should have a current release record before moving forward.",
		},
		{
			Code:   transitionBlockerSourceImage,
			Title:  "Source environment has a rendered image",
			Status: previewGateStatus(hasTransitionPreviewBlocker(blockersByCode, transitionBlockerSourceImage)),
			Detail: "Rendered source manifests must include an image tag for promotion or release.",
		},
		{
			Code:   transitionBlockerTargetMissing,
			Title:  "Target environment is available",
			Status: targetGateStatus,
			Detail: targetGateDetail,
		},
	}
}

func hasTransitionPreviewBlocker(
	blockersByCode map[string]TransitionPreviewBlocker,
	code string,
) bool {
	_, ok := blockersByCode[code]
	return ok
}

func transitionActionFromTarget(toEnvRaw string) string {
	if isProductionEnvironment(toEnvRaw) {
		return string(OpRelease)
	}
	return string(OpPromote)
}

func transitionActionFromStage(stage DeliveryStage) string {
	if stage == DeliveryStageRelease {
		return string(OpRelease)
	}
	return string(OpPromote)
}

func transitionRolloutPlan() []string {
	return []string{
		"promoter.plan",
		"promoter.render",
		"promoter.commit",
		"promoter.finalize",
	}
}

func transitionPreviewReleasePtr(release ReleaseRecord) *TransitionPreviewRelease {
	out := TransitionPreviewRelease{
		ID:            strings.TrimSpace(release.ID),
		Environment:   normalizeEnvironmentName(release.Environment),
		Image:         strings.TrimSpace(release.Image),
		OpKind:        release.OpKind,
		DeliveryStage: release.DeliveryStage,
		CreatedAt:     release.CreatedAt.UTC(),
	}
	return &out
}

func previewGateStatus(blocked bool) string {
	if blocked {
		return previewGateBlocked
	}
	return previewGatePassed
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
	return env == defaultReleaseEnvironment || env == "production"
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
