package tracker

import (
	"os"
	"strconv"
	"testing"

	"agentflow/internal/domain"
	"agentflow/internal/repo"
)

// TestGitLabLiveSmoke exercises the real GitLab Self-Managed REST path end to
// end: provider-token resolution, MR lookup by source branch, and delivery
// state refresh. It is env-gated and never runs in CI:
//
//	AGENTFLOW_GITLAB_LIVE_INSTANCE=https://git.example.com
//	AGENTFLOW_GITLAB_LIVE_TOKEN=glpat-...
//	AGENTFLOW_GITLAB_LIVE_PROJECT=group/sub/repo
//	AGENTFLOW_GITLAB_LIVE_BRANCH=source-branch-with-open-mr (optional)
//	AGENTFLOW_GITLAB_LIVE_MR_IID=123 (optional; skips FindBranchMR)
func TestGitLabLiveSmoke(t *testing.T) {
	instance := os.Getenv("AGENTFLOW_GITLAB_LIVE_INSTANCE")
	token := os.Getenv("AGENTFLOW_GITLAB_LIVE_TOKEN")
	fullPath := os.Getenv("AGENTFLOW_GITLAB_LIVE_PROJECT")
	if instance == "" || token == "" || fullPath == "" {
		t.Skip("GitLab live smoke requires AGENTFLOW_GITLAB_LIVE_INSTANCE/TOKEN/PROJECT")
	}
	key := domain.NormalizeGitLabInstanceKey(instance)
	if key == "" {
		t.Fatalf("invalid instance %q", instance)
	}
	_, _, ref, ok := domain.ParseGitLabRepoURL(key + "/" + fullPath)
	if !ok {
		t.Fatalf("invalid project %q", fullPath)
	}

	st, err := repo.OpenTest()
	if err != nil {
		t.Fatalf("OpenTest: %v", err)
	}
	defer st.Close()
	// OpenTest seeds project "platform" owned by user 1.
	if _, err := st.SaveProviderToken(1, ProviderGitLab, key, domain.ProviderCredentialPAT, token, "", nil); err != nil {
		t.Fatalf("SaveProviderToken: %v", err)
	}
	if err := st.PutRepository(domain.Repository{
		ID: "live-gitlab-repo", ProjectID: "platform", Provider: ProviderGitLab, Name: "live",
		GongfengProjectID: ref, WebURL: domain.GitLabWebURL(key, fullPath),
		CloneURL: domain.GitLabWebURL(key, fullPath) + ".git", DefaultBranch: "main",
	}); err != nil {
		t.Fatalf("PutRepository: %v", err)
	}

	svc := NewService()
	svc.ConfigureWithStore(domain.SyncConfig{}, st)

	branch := os.Getenv("AGENTFLOW_GITLAB_LIVE_BRANCH")
	iid := 0
	if branch != "" {
		mr, found, err := svc.FindBranchMR(ref, branch)
		if err != nil {
			t.Fatalf("FindBranchMR: %v", err)
		}
		if !found {
			t.Fatalf("no MR found for source branch %q", branch)
		}
		t.Logf("FindBranchMR: !%d state=%s url=%s", mr.IID, mr.State, mr.URL)
		iid = mr.IID
	}
	if iid == 0 {
		if v := os.Getenv("AGENTFLOW_GITLAB_LIVE_MR_IID"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				t.Fatalf("bad MR iid %q", v)
			}
			iid = n
		}
	}
	if iid == 0 {
		t.Skip("no MR to refresh; set AGENTFLOW_GITLAB_LIVE_BRANCH or _MR_IID")
	}

	url := domain.GitLabWebURL(key, fullPath) + "/-/merge_requests/" + strconv.Itoa(iid)
	if err := st.EnsureMRDeliveryState(ref, iid, url, "live-issue"); err != nil {
		t.Fatalf("EnsureMRDeliveryState: %v", err)
	}
	if err := svc.RefreshGitLabDelivery(st, ref, iid); err != nil {
		t.Fatalf("RefreshGitLabDelivery: %v", err)
	}
	rows, err := st.ListMRDeliveryStates()
	if err != nil {
		t.Fatalf("ListMRDeliveryStates: %v", err)
	}
	for _, row := range rows {
		if row.MRProject == ref && row.MRIID == iid {
			t.Logf("delivery state: %s target=%s", row.State, row.TargetBranch)
			if row.State == "" || row.State == "unknown" {
				t.Fatalf("refreshed state is %q", row.State)
			}
			return
		}
	}
	t.Fatalf("delivery row for %s!%d not found", ref, iid)
}
