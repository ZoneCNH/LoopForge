package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow/internal/domain"
	"agentflow/internal/repo"

	"github.com/gin-gonic/gin"
)

func gitlabWebhookContext(t *testing.T, body, token string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/api/hooks/gitlab", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Gitlab-Token", token)
	}
	c.Request = req
	return c, recorder
}

func TestGitLabWebhookSecretDisabled(t *testing.T) {
	t.Setenv(gitlabWebhookSecretEnv, "")
	h := &gitlabWebhookHandlers{deps: &deps{}}
	c, recorder := gitlabWebhookContext(t, `{}`, "anything")
	h.mergeRequest(c)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when secret unset, got %d", recorder.Code)
	}
}

func TestGitLabWebhookRejectsBadToken(t *testing.T) {
	t.Setenv(gitlabWebhookSecretEnv, "s3cret")
	h := &gitlabWebhookHandlers{deps: &deps{}}
	for _, token := range []string{"", "wrong", "s3cret-extra"} {
		c, recorder := gitlabWebhookContext(t, `{}`, token)
		h.mergeRequest(c)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("token %q: expected 401, got %d", token, recorder.Code)
		}
	}
}

func TestGitLabWebhookIgnoresNonMREvent(t *testing.T) {
	t.Setenv(gitlabWebhookSecretEnv, "s3cret")
	h := &gitlabWebhookHandlers{deps: &deps{repo: mustOpenTestStore(t)}}
	body := `{"object_kind":"note","object_attributes":{"iid":1},"project":{"web_url":"https://gitlab.example.com/g/r"}}`
	c, recorder := gitlabWebhookContext(t, body, "s3cret")
	h.mergeRequest(c)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "ignored") {
		t.Fatalf("expected 200 ignored, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestGitLabWebhookUnknownProjectIgnored(t *testing.T) {
	t.Setenv(gitlabWebhookSecretEnv, "s3cret")
	st := mustOpenTestStore(t)
	defer st.Close()
	h := &gitlabWebhookHandlers{deps: &deps{repo: st}}
	body := `{"object_kind":"merge_request","object_attributes":{"iid":7,"action":"open","url":"https://gitlab.example.com/g/r/-/merge_requests/7"},"project":{"path_with_namespace":"g/r","web_url":"https://gitlab.example.com/g/r"}}`
	c, recorder := gitlabWebhookContext(t, body, "s3cret")
	h.mergeRequest(c)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "project not registered") {
		t.Fatalf("expected 200 project-not-registered, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestGitLabWebhookRegisteredProjectWithoutSync(t *testing.T) {
	t.Setenv(gitlabWebhookSecretEnv, "s3cret")
	st := mustOpenTestStore(t)
	defer st.Close()
	_, _, ref, ok := domain.ParseGitLabRepoURL("https://gitlab.example.com/g/r")
	if !ok {
		t.Fatal("fixture ref parse failed")
	}
	if err := st.PutRepository(domain.Repository{
		ID: "repo-gitlab-1", ProjectID: "platform", Provider: "gitlab", Name: "r",
		GongfengProjectID: ref, WebURL: "https://gitlab.example.com/g/r",
		CloneURL: "https://gitlab.example.com/g/r.git", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("PutRepository: %v", err)
	}
	h := &gitlabWebhookHandlers{deps: &deps{repo: st}}
	body := `{"object_kind":"merge_request","object_attributes":{"iid":7,"action":"open","url":"https://gitlab.example.com/g/r/-/merge_requests/7"},"project":{"path_with_namespace":"g/r","web_url":"https://gitlab.example.com/g/r"}}`
	c, recorder := gitlabWebhookContext(t, body, "s3cret")
	h.mergeRequest(c)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without tracker service, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func mustOpenTestStore(t *testing.T) *repo.Store {
	t.Helper()
	st, err := repo.OpenTest()
	if err != nil {
		t.Fatalf("OpenTest: %v", err)
	}
	return st
}
