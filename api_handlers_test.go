//nolint:exhaustruct // Test fixtures intentionally use partial structs for readability.
package platform_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	platform "github.com/a2y-d5l/go-web-nats"
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

func TestAPI_HandleProjectByIDUnknownSubresourceReturnsNotFound(t *testing.T) {
	api := platform.NewTestAPI(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/unknown", nil)
	rec := httptest.NewRecorder()

	platform.InvokeHandleProjectByIDForTest(api, rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestAPI_HandleProjectByIDArtifactsDelegates(t *testing.T) {
	artifacts := newMemArtifacts()
	if _, err := artifacts.WriteFile("p1", "build/config.yaml", []byte("ok")); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	api := platform.NewTestAPI(artifacts)

	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/artifacts", nil)
	rec := httptest.NewRecorder()
	platform.InvokeHandleProjectByIDForTest(api, rec, req)

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

func TestAPI_HandleProjectArtifactsInvalidRouteReturnsNotFound(t *testing.T) {
	api := platform.NewTestAPI(newMemArtifacts())
	req := httptest.NewRequest(http.MethodGet, "/api/projects/p1/not-artifacts", nil)
	rec := httptest.NewRecorder()

	platform.InvokeHandleProjectArtifactsForTest(api, rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
