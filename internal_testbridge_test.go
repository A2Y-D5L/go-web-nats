//nolint:testpackage // External tests call these wrappers; bridge must access unexported internals.
package platform

import (
	"context"
	"net/http"
	"sync"
)

const (
	ProjectAPIVersionForTest = projectAPIVersion
	ProjectKindForTest       = projectKind
)

func NewTestAPI(artifacts ArtifactStore) *API {
	return &API{
		nc:              nil,
		store:           nil,
		artifacts:       artifacts,
		waiters:         nil,
		sourceTriggerMu: sync.Mutex{},
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

func MarkSourceCommitSeenForTest(api *API, projectID, commit string) (bool, error) {
	return api.markSourceCommitSeen(projectID, commit)
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
