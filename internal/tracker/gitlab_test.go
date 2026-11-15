package tracker

import (
	"agentflow/internal/domain"
	"agentflow/internal/repo"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newGitLabFixture wires a Service against a fake GitLab API and an
// OpenTest store whose project owner holds a provider token for the
// instance. It returns the service, store, project ref and repository.
func newGitLabFixture(t *testing.T, handler http.HandlerFunc) (*Service, *repo.Store, string, domain.Repository) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	st, err := repo.OpenTest()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.PutUser(domain.User{LoginName: "gitlab-owner"}); err != nil {
		t.Fatal(err)
	}
	u, _ := st.UserByLoginName("gitlab-owner")
	if _, err := st.SaveProviderToken(u.ID, "gitlab", srv.URL, "pat", "glpat-test-token", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.PutProject(domain.Project{ID: "gl", Name: "GitLab", OwnerID: u.ID}); err != nil {
		t.Fatal(err)
	}
	key, path, ref, ok := domain.ParseGitLabRepoURL(srv.URL + "/group/sub/repo")
	if !ok {
		t.Fatalf("fixture ref: %q %q", key, path)
	}
	r := domain.Repository{
		ID: "gitlab-repo", ProjectID: "gl", Provider: "gitlab", Name: "repo",
		GongfengProjectID: ref, DefaultBranch: "main",
		WebURL:   domain.GitLabWebURL(key, path),
		CloneURL: domain.GitLabWebURL(key, path) + ".git",
		ExternalRef: &domain.RepositoryExternalRef{
			ProviderKind: "gitlab", InstanceKey: key, FullPath: path,
			APIBaseURL: key + "/api/v4",
		},
	}
	if err := st.PutRepository(r); err != nil {
		t.Fatal(err)
	}
	s := NewService()
	s.ConfigureWithStore(domain.SyncConfig{}, st)
	return s, st, ref, r
}

func TestGitLabPullIssuesAndLocalWorkPreserved(t *testing.T) {
	s, st, ref, r := newGitLabFixture(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("PRIVATE-TOKEN") != "glpat-test-token" {
			t.Errorf("missing PRIVATE-TOKEN header")
		}
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "GET" && strings.HasSuffix(req.URL.Path, "/issues") {
			_, _ = w.Write([]byte(`[{"id":501,"iid":7,"title":"Remote","description":"brief","state":"opened","web_url":"https://gitlab.example.com/group/sub/repo/-/issues/7","created_at":"2026-11-01T00:00:00Z","updated_at":"2026-11-01T00:00:00Z"}]`))
			return
		}
		t.Errorf("unexpected %s %s", req.Method, req.URL.Path)
		w.WriteHeader(404)
	})
	n, err := s.pullGitLabIssues(st, r)
	if err != nil || n != 1 {
		t.Fatalf("pull: %d %v", n, err)
	}
	issue, ok := st.Issue("gitlab-gitlab-repo-7")
	if !ok || issue.Source != "gitlab" || issue.ExternalRef == nil || issue.ExternalRef.ExternalState != "opened" {
		t.Fatalf("issue: %+v", issue)
	}
	issue.Description = "local clarification"
	issue.Status = "in_progress"
	if err := st.PutIssue(issue); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pullGitLabIssues(st, r); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.Issue(issue.ID); got.Description != issue.Description || got.Status != "in_progress" {
		t.Fatalf("local work overwritten: %+v", got)
	}
	_ = ref
}

func TestGitLabMergeBranchCreatesAndMerges(t *testing.T) {
	var calls []string
	s, _, ref, _ := newGitLabFixture(t, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := req.URL.Path
		switch {
		case req.Method == "GET" && strings.HasSuffix(path, "/merge_requests"):
			calls = append(calls, "GET list")
			_, _ = w.Write([]byte(`[]`))
		case req.Method == "POST" && strings.HasSuffix(path, "/merge_requests"):
			calls = append(calls, "POST create")
			var body map[string]any
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body["source_branch"] != "feature/x" || body["target_branch"] != "main" {
				t.Errorf("create body: %v", body)
			}
			_, _ = w.Write([]byte(`{"id":900,"iid":3,"state":"opened","source_branch":"feature/x","target_branch":"main","sha":"head-sha","detailed_merge_status":"checking","web_url":"https://gitlab.example.com/group/sub/repo/-/merge_requests/3"}`))
		case req.Method == "GET" && strings.HasSuffix(path, "/merge_requests/3"):
			calls = append(calls, "GET fresh")
			_, _ = w.Write([]byte(`{"id":900,"iid":3,"state":"opened","source_branch":"feature/x","target_branch":"main","sha":"head-sha","detailed_merge_status":"mergeable"}`))
		case req.Method == "PUT" && strings.HasSuffix(path, "/merge_requests/3/merge"):
			calls = append(calls, "PUT merge")
			var body map[string]any
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body["sha"] != "head-sha" {
				t.Errorf("merge must bind observed sha, got %v", body)
			}
			_, _ = w.Write([]byte(`{"state":"merged"}`))
		default:
			t.Errorf("unexpected %s %s", req.Method, path)
			w.WriteHeader(404)
		}
	})
	refOut, err := s.EnsureAndMergeBranchMR(ref, "feature/x", "main", "Title", "")
	if err != nil || refOut != "!3" {
		t.Fatalf("merge: %q %v", refOut, err)
	}
	want := []string{"GET list", "POST create", "GET fresh", "PUT merge"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v", calls)
	}
}

func TestGitLabMergeBranchRefusesNonMergeable(t *testing.T) {
	s, _, ref, _ := newGitLabFixture(t, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == "GET" && strings.HasSuffix(req.URL.Path, "/merge_requests"):
			_, _ = w.Write([]byte(`[{"id":900,"iid":3,"state":"opened","source_branch":"feature/x","target_branch":"main","sha":"s","detailed_merge_status":"mergeable"}]`))
		case req.Method == "GET" && strings.HasSuffix(req.URL.Path, "/merge_requests/3"):
			_, _ = w.Write([]byte(`{"id":900,"iid":3,"state":"opened","source_branch":"feature/x","target_branch":"main","sha":"s","detailed_merge_status":"conflict"}`))
		default:
			t.Errorf("unexpected %s %s", req.Method, req.URL.Path)
			w.WriteHeader(404)
		}
	})
	if _, err := s.EnsureAndMergeBranchMR(ref, "feature/x", "main", "", ""); err == nil {
		t.Fatal("conflicting MR must not merge")
	}
}

func TestGitLabMergeBranchAsyncStatusIsConflict(t *testing.T) {
	s, _, ref, _ := newGitLabFixture(t, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == "GET" && strings.HasSuffix(req.URL.Path, "/merge_requests"):
			_, _ = w.Write([]byte(`[{"id":900,"iid":3,"state":"opened","source_branch":"feature/x","target_branch":"main","sha":"s","detailed_merge_status":"checking"}]`))
		case req.Method == "GET" && strings.HasSuffix(req.URL.Path, "/merge_requests/3"):
			_, _ = w.Write([]byte(`{"id":900,"iid":3,"state":"opened","source_branch":"feature/x","target_branch":"main","sha":"s","detailed_merge_status":"checking"}`))
		default:
			w.WriteHeader(404)
		}
	})
	if _, err := s.EnsureAndMergeBranchMR(ref, "feature/x", "main", "", ""); err == nil {
		t.Fatal("checking status must surface as conflict, not merge")
	}
}

func TestGitLabFindBranchAndStateMapping(t *testing.T) {
	s, _, ref, _ := newGitLabFixture(t, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "GET" && strings.HasSuffix(req.URL.Path, "/merge_requests") {
			_, _ = w.Write([]byte(`[{"id":900,"iid":5,"state":"opened","source_branch":"feature/y","target_branch":"main","web_url":"https://gitlab.example.com/group/sub/repo/-/merge_requests/5"}]`))
			return
		}
		t.Errorf("unexpected %s %s", req.Method, req.URL.Path)
		w.WriteHeader(404)
	})
	mr, ok, err := s.FindBranchMR(ref, "feature/y")
	if err != nil || !ok {
		t.Fatalf("find: %v %v", ok, err)
	}
	if mr.IID != 5 || mr.State != "opened" || mr.Project != ref {
		t.Fatalf("mr: %+v", mr)
	}
	// 状态映射：merged/closed/unknown 归一化
	if gitLabMRState("merged") != "merged" || gitLabMRState("closed") != "closed" || gitLabMRState("weird") != "unknown" {
		t.Fatal("state mapping")
	}
	if gitLabMergeStatus("checking") != "unknown" || gitLabMergeStatus("mergeable") != "mergeable" || gitLabMergeStatus("conflict") != "blocked" {
		t.Fatal("merge status mapping")
	}
}

func TestGitLabAuthFailureSurfaces(t *testing.T) {
	s, _, ref, _ := newGitLabFixture(t, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	if _, _, err := s.FindBranchMR(ref, "b"); err == nil {
		t.Fatal("401 must surface as error")
	}
}
