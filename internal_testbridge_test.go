//nolint:testpackage // External tests call these wrappers; bridge must access unexported internals.
package platform

import (
	"context"
	"net/http"
)

const (
	ProjectAPIVersionForTest = projectAPIVersion
	ProjectKindForTest       = projectKind
)

func NewTestAPI(artifacts ArtifactStore) *API {
	return &API{
		nc:        nil,
		store:     nil,
		artifacts: artifacts,
		waiters:   nil,
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
