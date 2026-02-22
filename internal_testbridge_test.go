//nolint:testpackage // External tests call these wrappers; bridge must access unexported internals.
package platform

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
)

const (
	ProjectAPIVersionForTest = projectAPIVersion
	ProjectKindForTest       = projectKind
)

func NewTestAPI(artifacts ArtifactStore) *API {
	return &API{
		nc:                  nil,
		store:               nil,
		artifacts:           artifacts,
		waiters:             nil,
		opEvents:            nil,
		opHeartbeatInterval: 0,
		sourceTriggerMu:     sync.Mutex{},
	}
}

func InvokeHandleProjectByIDForTest(api *API, w http.ResponseWriter, r *http.Request) {
	api.handleProjectByID(w, r)
}

func InvokeHandleProjectArtifactsForTest(api *API, w http.ResponseWriter, r *http.Request) {
	api.handleProjectArtifacts(w, r)
}

func IsMainBranchWebhookForTest(branch, ref string) bool {
	return isMainBranchWebhook(branch, ref)
}

func NormalizeProjectSpecForTest(in ProjectSpec) ProjectSpec {
	return normalizeProjectSpec(in)
}

func ValidateProjectSpecForTest(spec ProjectSpec) error {
	return validateProjectSpec(spec)
}

func RenderProjectConfigYAMLForTest(spec ProjectSpec) []byte {
	return renderProjectConfigYAML(spec)
}

func RenderKustomizedProjectManifestsForTest(
	spec ProjectSpec,
	image string,
) (string, string, string, error) {
	rendered, err := renderKustomizedProjectManifests(spec, image)
	if err != nil {
		return "", "", "", err
	}
	return rendered.deployment, rendered.service, rendered.rendered, nil
}

type WaiterHubForTest struct {
	hub *waiterHub
}

func NewWaiterHubForTest() *WaiterHubForTest {
	return &WaiterHubForTest{
		hub: newWaiterHub(),
	}
}

func (h *WaiterHubForTest) Register(opID string) <-chan WorkerResultMsg {
	return h.hub.register(opID)
}

func (h *WaiterHubForTest) Unregister(opID string) {
	h.hub.unregister(opID)
}

func (h *WaiterHubForTest) Deliver(opID string, msg WorkerResultMsg) {
	h.hub.deliver(opID, msg)
}

func RenderSourceWebhookHookScriptForTest(projectID, endpoint string) string {
	return renderSourceWebhookHookScript(projectID, endpoint)
}

func CommitWatcherEnabledForTest() bool {
	return commitWatcherEnabled()
}

func ShouldSkipSourceCommitMessageForTest(message string) bool {
	return shouldSkipSourceCommitMessage(message)
}

func ShouldRecordWatcherSeenCommitForTest(accepted bool, reason string) bool {
	return shouldRecordWatcherSeenCommit(sourceRepoWebhookResult{
		accepted: accepted,
		reason:   reason,
		project:  "",
		op:       nil,
		commit:   "",
		trigger:  "",
	})
}

func MarkSourceCommitSeenForTest(api *API, projectID, commit string) (bool, error) {
	return api.markSourceCommitSeen(projectID, commit)
}

func RollbackSourceCommitPendingEnqueueForTest(api *API, projectID, commit string) error {
	return api.rollbackSourceCommitPendingEnqueue(projectID, commit)
}

func ConfirmSourceCommitPendingOpForTest(api *API, projectID, commit, opID string) error {
	return api.confirmSourceCommitPendingOp(projectID, commit, opID)
}

func FinalizeSourceCommitPendingOpForTest(
	api *API,
	projectID, opID string,
	successful bool,
) error {
	return finalizeSourceCommitPendingOp(api.artifacts, projectID, opID, successful)
}

func ProjectSupportsEnvironmentForTest(spec ProjectSpec, env string) bool {
	return projectSupportsEnvironment(spec, env)
}

func EnsureLocalGitRepoForTest(ctx context.Context, dir string) error {
	return ensureLocalGitRepo(ctx, dir)
}

func UpsertFileForTest(path string, data []byte) (bool, error) {
	return upsertFile(path, data)
}

func GitCommitIfChangedForTest(ctx context.Context, dir, message string) (bool, error) {
	return gitCommitIfChanged(ctx, dir, message)
}

func GitRevParseForTest(ctx context.Context, dir, ref string) (string, error) {
	return gitRevParse(ctx, dir, ref)
}

func ParseImageBuilderModeForTest(raw string) (string, error) {
	mode, err := parseImageBuilderMode(raw)
	return string(mode), err
}

func ImageBuilderModeFromEnvForTest() (string, error) {
	mode, err := imageBuilderModeFromEnv()
	return string(mode), err
}

func ResolveEffectiveImageBuilderModeForTest(
	raw string,
	explicit bool,
	buildkitAvailable bool,
	probeErrText string,
) (string, string, string, string, string) {
	requested, parseErr := parseImageBuilderMode(raw)
	resolution := resolveEffectiveImageBuilderModeWithProbe(
		context.Background(),
		requested,
		explicit,
		parseErr,
		buildkitAvailable,
		func(context.Context) error {
			return errorFromString(probeErrText)
		},
	)
	return string(resolution.requestedMode),
		string(resolution.effectiveMode),
		resolution.requestedWarning,
		resolution.fallbackReason,
		resolution.policyError
}

func ImageBuilderStepStartMessageForTest(
	raw string,
	explicit bool,
	buildkitAvailable bool,
	probeErrText string,
) string {
	requested, parseErr := parseImageBuilderMode(raw)
	resolution := resolveEffectiveImageBuilderModeWithProbe(
		context.Background(),
		requested,
		explicit,
		parseErr,
		buildkitAvailable,
		func(context.Context) error {
			return errorFromString(probeErrText)
		},
	)
	return imageBuilderStepStartMessage(resolution)
}

func SelectImageBuilderBackendNameForTest(modeRaw string) (string, error) {
	mode, err := parseImageBuilderMode(modeRaw)
	if err != nil {
		return "", err
	}
	return selectImageBuilderBackendName(mode), nil
}

func RunImageBuilderBuildForTest(
	ctx context.Context,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	imageTag string,
) (string, []string, error) {
	outcome, err := runImageBuilderBuild(ctx, artifacts, msg, spec, imageTag)
	return outcome.message, outcome.artifacts, err
}

func RunImageBuilderBuildWithBackendForTest(
	ctx context.Context,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	imageTag string,
	modeRaw string,
	message, summary, logs string,
	metadata map[string]any,
	errText string,
) (string, []string, error) {
	mode, modeErr := parseImageBuilderMode(modeRaw)
	modeResolution := imageBuilderModeResolution{
		requestedMode:     mode,
		requestedExplicit: true,
		effectiveMode:     mode,
		requestedWarning:  "",
		fallbackReason:    "",
		policyError:       "",
	}
	if modeErr != nil {
		modeResolution.requestedWarning = modeErr.Error()
	}
	dockerfileBody := renderImageBuilderDockerfile(spec)
	dockerfilePath, err := artifacts.WriteFile(msg.ProjectID, imageBuildDockerfilePath, dockerfileBody)
	if err != nil {
		return "", nil, err
	}
	outcome, runErr := runImageBuilderBuildWithBackend(
		ctx,
		artifacts,
		msg,
		modeResolution,
		testBuildKitBackend{
			outcome: imageBuildResult{
				message:  message,
				summary:  summary,
				metadata: metadata,
				logs:     logs,
			},
			err: errorFromString(errText),
		},
		imageBuildRequest{
			OpID:              msg.OpID,
			ProjectID:         msg.ProjectID,
			Spec:              spec,
			ImageTag:          imageTag,
			ContextDir:        sourceRepoDir(artifacts, msg.ProjectID),
			DockerfileBody:    dockerfileBody,
			DockerfileRelPath: imageBuildDockerfilePath,
		},
		[]string{dockerfilePath},
	)
	return outcome.message, outcome.artifacts, runErr
}

func RunManifestApplyForTest(
	ctx context.Context,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	imageTag string,
	targetEnv string,
) (string, []string, error) {
	outcome, err := runManifestApplyForEnvironment(
		ctx,
		nil,
		artifacts,
		msg,
		spec,
		imageTag,
		targetEnv,
	)
	return outcome.message, outcome.artifacts, err
}

func RunManifestPromotionForTest(
	ctx context.Context,
	artifacts ArtifactStore,
	msg ProjectOpMsg,
	spec ProjectSpec,
	fromEnv string,
	toEnv string,
) (string, []string, error) {
	outcome, err := runManifestPromotionForEnvironments(
		ctx,
		nil,
		artifacts,
		msg,
		spec,
		fromEnv,
		toEnv,
	)
	return outcome.message, outcome.artifacts, err
}

type testBuildKitBackend struct {
	outcome imageBuildResult
	err     error
}

func (testBuildKitBackend) name() string {
	return string(imageBuilderModeBuildKit)
}

func (b testBuildKitBackend) build(
	ctx context.Context,
	_ imageBuildRequest,
) (imageBuildResult, error) {
	if err := ensureContextAlive(ctx); err != nil {
		return imageBuildResult{}, err
	}
	return b.outcome, b.err
}

func errorFromString(text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return errors.New(text)
}
