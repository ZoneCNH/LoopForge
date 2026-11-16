package httpapi

import (
	"net/http"
	"path/filepath"
	"testing"

	"agentflow/internal/engine"
	"agentflow/internal/repo"
	"agentflow/internal/runtime"
	"agentflow/internal/tracker"
)

// Shared assets own no project (the seeded platform image and sandbox work that
// way), so the API must let an admin maintain them without moving them into a
// project.
func TestSharedAssetPUTAcceptsEmptyProjectID(t *testing.T) {
	root := t.TempDir()
	s, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h := New(s, engine.New(s, runtime.NewManager(filepath.Join(root, "workspaces"))), tracker.NewService(), "", "")

	requestJSON[map[string]string](t, h, http.MethodPut, "/api/dockerfiles", map[string]any{"id": "shared-slim", "name": "Shared slim"}, http.StatusBadRequest)

	saved := requestJSON[map[string]bool](t, h, http.MethodPut, "/api/dockerfiles", map[string]any{
		"id": "shared-slim", "name": "Shared slim", "shared": true, "content": "FROM debian:bookworm-slim",
	}, http.StatusOK)
	if !saved["ok"] {
		t.Fatalf("shared dockerfile save = %+v", saved)
	}
	dockerfile, ok := s.Dockerfile("shared-slim")
	if !ok || !dockerfile.Shared || dockerfile.ProjectID != "" {
		t.Fatalf("shared dockerfile = %+v, want shared with empty projectId", dockerfile)
	}

	requestJSON[map[string]bool](t, h, http.MethodPut, "/api/sandboxes", map[string]any{
		"id": "shared-container", "name": "Shared container", "shared": true, "driver": "docker", "dockerfileId": "shared-slim",
	}, http.StatusOK)
	sandbox, ok := s.Sandbox("shared-container")
	if !ok || !sandbox.Shared || sandbox.ProjectID != "" {
		t.Fatalf("shared sandbox = %+v, want shared with empty projectId", sandbox)
	}

	requestJSON[map[string]string](t, h, http.MethodPut, "/api/dockerfiles", map[string]any{"id": "orphan", "name": "Orphan", "content": "FROM scratch"}, http.StatusBadRequest)
}
