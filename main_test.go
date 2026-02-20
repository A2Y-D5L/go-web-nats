//nolint:exhaustruct,govet // Tests favor compact fixtures and table syntax over exhaustive struct initialization and strict style rules.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type memArtifacts struct {
	mu    sync.Mutex
	files map[string]map[string][]byte
}

func newMemArtifacts() *memArtifacts {
	return &memArtifacts{
		files: map[string]map[string][]byte{},
	}
}

func (m *memArtifacts) ProjectDir(projectID string) string {
	return filepath.Join("/tmp", "artifacts", projectID)
}

func (m *memArtifacts) EnsureProjectDir(projectID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[projectID]; !ok {
		m.files[projectID] = map[string][]byte{}
	}
	return m.ProjectDir(projectID), nil
}

func (m *memArtifacts) WriteFile(projectID, relPath string, data []byte) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[projectID]; !ok {
		m.files[projectID] = map[string][]byte{}
	}
	m.files[projectID][relPath] = append([]byte(nil), data...)
	return relPath, nil
}

func (m *memArtifacts) ListFiles(projectID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.files[projectID]
	if !ok {
		return []string{}, nil
	}
	out := make([]string, 0, len(project))
	for k := range project {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (m *memArtifacts) ReadFile(projectID, relPath string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.files[projectID]
	if !ok {
		return nil, os.ErrNotExist
	}
	data, ok := project[relPath]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), data...), nil
}

func (m *memArtifacts) RemoveProject(projectID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, projectID)
	return nil
}

func TestWaiterHubRegisterDeliver(t *testing.T) {
	h := newWaiterHub()
	ch := h.register("op-1")

	h.deliver("op-1", WorkerResultMsg{OpID: "op-1", Message: "done"})

	select {
	case got := <-ch:
		if got.OpID != "op-1" {
			t.Fatalf("unexpected op id: got %q", got.OpID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for waiter delivery")
	}
}

func TestWaiterHubUnregisterAndDeliverNoPanic(_ *testing.T) {
	h := newWaiterHub()

	for range 100 {
		opID := "op-race"
		h.register(opID)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 200 {
				h.deliver(opID, WorkerResultMsg{OpID: opID})
			}
		}()
		go func() {
			defer wg.Done()
			h.unregister(opID)
		}()
		wg.Wait()
	}
}

func TestHandleProjectByIDUnknownSubresourceReturnsNotFound(t *testing.T) {
	api := &API{}
	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/unknown", nil)
	rec := httptest.NewRecorder()

	api.handleProjectByID(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleProjectByIDArtifactsDelegates(t *testing.T) {
	artifacts := newMemArtifacts()
	if _, err := artifacts.WriteFile("p1", "build/config.yaml", []byte("ok")); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	api := &API{artifacts: artifacts}

	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/artifacts", nil)
	rec := httptest.NewRecorder()
	api.handleProjectByID(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%q", rec.Code, rec.Body.String())
	}

	var body struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	if len(body.Files) != 1 || body.Files[0] != "build/config.yaml" {
		t.Fatalf("unexpected files payload: %#v", body.Files)
	}
}

func TestHandleProjectArtifactsInvalidRouteReturnsNotFound(t *testing.T) {
	api := &API{artifacts: newMemArtifacts()}
	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/not-artifacts", nil)
	rec := httptest.NewRecorder()

	api.handleProjectArtifacts(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestWorkerResultCarriesOpKindAndSpecForNextWorker(t *testing.T) {
	in := WorkerResultMsg{
		OpID:      "op-1",
		Kind:      OpCreate,
		ProjectID: "proj-1",
		Spec: ProjectSpec{
			APIVersion: projectAPIVersion,
			Kind:       projectKind,
			Name:       "svc",
			Runtime:    "go_1.26",
			Environments: map[string]EnvConfig{
				"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
			},
			NetworkPolicies: NetworkPolicies{
				Ingress: "internal",
				Egress:  "internal",
			},
		},
	}

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal worker result: %v", err)
	}

	var opMsg ProjectOpMsg
	if err := json.Unmarshal(b, &opMsg); err != nil {
		t.Fatalf("unmarshal as project op: %v", err)
	}

	if opMsg.Kind != OpCreate {
		t.Fatalf("expected kind %q, got %q", OpCreate, opMsg.Kind)
	}
	if opMsg.Spec.Name != "svc" || opMsg.Spec.Runtime != "go_1.26" {
		t.Fatalf("unexpected spec: %#v", opMsg.Spec)
	}
}

func TestNormalizeProjectSpecDefaults(t *testing.T) {
	spec := normalizeProjectSpec(ProjectSpec{
		Name:    "hello",
		Runtime: "go_1.26",
		Environments: map[string]EnvConfig{
			"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
		},
	})

	if spec.APIVersion != projectAPIVersion {
		t.Fatalf("expected apiVersion %q, got %q", projectAPIVersion, spec.APIVersion)
	}
	if spec.Kind != projectKind {
		t.Fatalf("expected kind %q, got %q", projectKind, spec.Kind)
	}
	if spec.NetworkPolicies.Ingress != "internal" || spec.NetworkPolicies.Egress != "internal" {
		t.Fatalf("unexpected default network policies: %#v", spec.NetworkPolicies)
	}
}

func TestValidateProjectSpecRejectsBadRuntime(t *testing.T) {
	spec := normalizeProjectSpec(ProjectSpec{
		Name:    "hello",
		Runtime: "go 1.26",
		Environments: map[string]EnvConfig{
			"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
		},
		NetworkPolicies: NetworkPolicies{
			Ingress: "internal",
			Egress:  "internal",
		},
	})
	err := validateProjectSpec(spec)
	if err == nil {
		t.Fatal("expected runtime validation error")
	}
	if !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("expected runtime error, got %v", err)
	}
}

func TestRenderProjectConfigYAML(t *testing.T) {
	spec := normalizeProjectSpec(ProjectSpec{
		Name:    "hello",
		Runtime: "go_1.26",
		Environments: map[string]EnvConfig{
			"dev": {Vars: map[string]string{"LOG_LEVEL": "info"}},
		},
		NetworkPolicies: NetworkPolicies{
			Ingress: "internal",
			Egress:  "internal",
		},
	})
	out := string(renderProjectConfigYAML(spec))
	if !strings.Contains(out, "apiVersion: "+projectAPIVersion) {
		t.Fatalf("missing apiVersion in yaml: %s", out)
	}
	if !strings.Contains(out, "runtime: go_1.26") {
		t.Fatalf("missing runtime in yaml: %s", out)
	}
	if !strings.Contains(out, "networkPolicies:") {
		t.Fatalf("missing networkPolicies in yaml: %s", out)
	}
}

func TestIsMainBranchWebhook(t *testing.T) {
	cases := []struct {
		branch string
		ref    string
		want   bool
	}{
		{branch: "main", want: true},
		{branch: "Main", want: true},
		{branch: "refs/heads/main", want: true},
		{branch: "feature/test", want: false},
		{ref: "refs/heads/main", want: true},
		{ref: "heads/main", want: true},
		{ref: "refs/heads/dev", want: false},
		{ref: "main", want: true},
		{branch: "feature/x", ref: "refs/heads/main", want: true},
	}
	for _, tc := range cases {
		got := isMainBranchWebhook(tc.branch, tc.ref)
		if got != tc.want {
			t.Fatalf("isMainBranchWebhook(%q,%q)=%v want %v", tc.branch, tc.ref, got, tc.want)
		}
	}
}

func TestRenderSourceWebhookHookScript(t *testing.T) {
	endpoint := "http://127.0.0.1:8080/api/webhooks/source"
	script := renderSourceWebhookHookScript("project-123", endpoint)

	if !strings.Contains(script, endpoint) {
		t.Fatalf("hook script missing endpoint: %s", script)
	}
	if !strings.Contains(script, `\"project_id\":\"project-123\"`) {
		t.Fatalf("hook script missing project id payload: %s", script)
	}
	if !strings.Contains(script, "platform-sync:*") {
		t.Fatalf("hook script missing platform-sync skip guard: %s", script)
	}
}

func TestEnsureLocalGitRepoAndCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}

	repo := filepath.Join(t.TempDir(), "source")
	if err := ensureLocalGitRepo(context.Background(), repo); err != nil {
		t.Fatalf("ensure local git repo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatalf("missing .git dir: %v", err)
	}

	changed, err := upsertFile(filepath.Join(repo, "README.md"), []byte("# test\n"))
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	if !changed {
		t.Fatal("expected file to be created")
	}

	committed, err := gitCommitIfChanged(
		context.Background(),
		repo,
		"platform-sync: seed test repo",
	)
	if err != nil {
		t.Fatalf("git commit if changed: %v", err)
	}
	if !committed {
		t.Fatal("expected commit to be created")
	}

	head, err := gitRevParse(context.Background(), repo, "HEAD")
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	if len(strings.TrimSpace(head)) < 8 {
		t.Fatalf("unexpected HEAD hash: %q", head)
	}
}

func TestFSArtifactsListFilesSkipsGitDirectories(t *testing.T) {
	root := t.TempDir()
	artifacts := NewFSArtifacts(root)

	projectID := "p1"
	projectDir, err := artifacts.EnsureProjectDir(projectID)
	if err != nil {
		t.Fatalf("ensure project dir: %v", err)
	}
	if _, err := artifacts.WriteFile(projectID, "repos/source/main.go", []byte("package main\n")); err != nil {
		t.Fatalf("write file: %v", err)
	}

	gitConfig := filepath.Join(projectDir, "repos", "source", ".git", "config")
	if err := os.MkdirAll(filepath.Dir(gitConfig), 0o755); err != nil {
		t.Fatalf("mkdir git dir: %v", err)
	}
	if err := os.WriteFile(gitConfig, []byte("[core]\n"), 0o644); err != nil {
		t.Fatalf("write git config: %v", err)
	}

	files, err := artifacts.ListFiles(projectID)
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	for _, f := range files {
		if strings.Contains(f, "/.git/") || strings.HasPrefix(f, ".git/") {
			t.Fatalf("expected .git paths to be filtered, got %q", f)
		}
	}
	if len(files) != 1 || files[0] != "repos/source/main.go" {
		t.Fatalf("unexpected file list: %#v", files)
	}
}
