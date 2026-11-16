package tracker

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

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
	found := false
	for _, row := range rows {
		if row.MRProject == ref && row.MRIID == iid {
			found = true
			t.Logf("delivery state: %s target=%s", row.State, row.TargetBranch)
			if row.State == "" || row.State == "unknown" {
				t.Fatalf("refreshed state is %q", row.State)
			}
		}
	}
	if !found {
		t.Fatalf("delivery row for %s!%d not found", ref, iid)
	}
	// Issue 拉取路径同样走真实 API；项目无 issue 时预期导入 0 条且不报错。
	// 直接调 pullGitLabIssues，避免 PullAll 被项目 TAPD 配置干扰。
	var liveRepo domain.Repository
	for _, r := range st.Repositories() {
		if r.GongfengProjectID == ref {
			liveRepo = r
		}
	}
	if liveRepo.ID == "" {
		t.Fatal("live repository not visible to store")
	}
	imported, err := svc.pullGitLabIssues(st, liveRepo)
	if err != nil {
		t.Fatalf("pullGitLabIssues: %v", err)
	}
	t.Logf("pullGitLabIssues imported %d issues", imported)
}

// TestGitLabLiveMRMerge creates a real branch + commit + MR on the configured
// Self-Managed project, merges it through EnsureAndMergeBranchMR, verifies the
// merged delivery state, then deletes the source branch. Env-gated:
//
//	AGENTFLOW_GITLAB_LIVE_INSTANCE / _TOKEN / _PROJECT (same as smoke test)
//	AGENTFLOW_GITLAB_LIVE_TARGET=main (optional, defaults to repository default)
//	AGENTFLOW_GITLAB_LIVE_MERGE=1 (required opt-in: performs real writes)
func TestGitLabLiveMRMerge(t *testing.T) {
	if os.Getenv("AGENTFLOW_GITLAB_LIVE_MERGE") != "1" {
		t.Skip("set AGENTFLOW_GITLAB_LIVE_MERGE=1 to run the real MR merge scenario")
	}
	instance := os.Getenv("AGENTFLOW_GITLAB_LIVE_INSTANCE")
	token := os.Getenv("AGENTFLOW_GITLAB_LIVE_TOKEN")
	fullPath := os.Getenv("AGENTFLOW_GITLAB_LIVE_PROJECT")
	if instance == "" || token == "" || fullPath == "" {
		t.Skip("GitLab live MR merge requires AGENTFLOW_GITLAB_LIVE_INSTANCE/TOKEN/PROJECT")
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
	if _, err := st.SaveProviderToken(1, ProviderGitLab, key, domain.ProviderCredentialPAT, token, "", nil); err != nil {
		t.Fatalf("SaveProviderToken: %v", err)
	}
	target := strings.TrimSpace(os.Getenv("AGENTFLOW_GITLAB_LIVE_TARGET"))
	if target == "" {
		target = "main"
	}
	if err := st.PutRepository(domain.Repository{
		ID: "live-gitlab-mr-repo", ProjectID: "platform", Provider: ProviderGitLab, Name: "live-mr",
		GongfengProjectID: ref, WebURL: domain.GitLabWebURL(key, fullPath),
		CloneURL: domain.GitLabWebURL(key, fullPath) + ".git", DefaultBranch: target,
	}); err != nil {
		t.Fatalf("PutRepository: %v", err)
	}

	svc := NewService()
	svc.ConfigureWithStore(domain.SyncConfig{}, st)
	client, extRef, err := svc.gitlabClientForProject(ref)
	if err != nil {
		t.Fatalf("gitlabClientForProject: %v", err)
	}

	branch := "agentflow-e2e-mr-" + strconv.FormatInt(time.Now().Unix(), 10)
	// 建分支 → 提交文件 → EnsureAndMergeBranchMR 创建并合并 MR → 校验 → 删分支。
	if err := client.request("POST", extRef.FullPath, "/repository/branches",
		url.Values{"branch": {branch}, "ref": {target}}, nil, nil); err != nil {
		t.Fatalf("create branch: %v", err)
	}
	defer func() {
		_ = client.request("DELETE", extRef.FullPath, "/repository/branches/"+url.PathEscape(branch), nil, nil, nil)
	}()
	// commit-msg CI job enforces `type(scope): subject` on commit AND MR title.
	commit := map[string]any{
		"branch":         branch,
		"commit_message": "chore(e2e): agentflow mr merge check",
		"actions":        []map[string]any{{"action": "create", "file_path": "agentflow-e2e/mr-merge-check.txt", "content": "e2e\n"}},
	}
	if err := client.request("POST", extRef.FullPath, "/repository/commits", nil, commit, nil); err != nil {
		t.Fatalf("create commit: %v", err)
	}

	// GitLab computes mergeability asynchronously and the project enforces
	// pipeline success; both surface as retryable ErrProviderConflict, so poll
	// until the MR becomes mergeable (pipelines on this project take minutes).
	var mergedRef string
	for attempt := 0; attempt < 240; attempt++ {
		mergedRef, err = svc.EnsureAndMergeBranchMR(ref, branch, target, "chore(e2e): agentflow mr merge", "automated live test")
		if !errors.Is(err, ErrProviderConflict) {
			break
		}
		t.Logf("merge attempt %d: conflict (mergeability/pipeline pending), retrying", attempt+1)
		time.Sleep(15 * time.Second)
	}
	if err != nil {
		t.Fatalf("EnsureAndMergeBranchMR: %v", err)
	}
	t.Logf("EnsureAndMergeBranchMR returned %s", mergedRef)

	// On pipeline-required projects the merge may be armed via
	// merge-when-pipeline-succeeds and land asynchronously; poll FindBranchMR
	// until the MR reports merged.
	var mr domain.DeliveryMR
	found := false
	for attempt := 0; attempt < 120; attempt++ {
		mr, found, err = svc.FindBranchMR(ref, branch)
		if err != nil {
			t.Fatalf("FindBranchMR after merge: %v", err)
		}
		if found && mr.State == "merged" {
			break
		}
		if attempt%8 == 0 {
			t.Logf("waiting for MR merge, state=%q found=%v", mr.State, found)
		}
		time.Sleep(15 * time.Second)
	}
	if !found {
		t.Fatal("merged MR not found by source branch")
	}
	if mr.State != "merged" {
		t.Fatalf("MR !%d state = %q, want merged", mr.IID, mr.State)
	}
	t.Logf("merged MR !%d url=%s", mr.IID, mr.URL)

	if err := st.EnsureMRDeliveryState(ref, mr.IID, mr.URL, "live-mr-issue"); err != nil {
		t.Fatalf("EnsureMRDeliveryState: %v", err)
	}
	if err := svc.RefreshGitLabDelivery(st, ref, mr.IID); err != nil {
		t.Fatalf("RefreshGitLabDelivery: %v", err)
	}
	rows, err := st.ListMRDeliveryStates()
	if err != nil {
		t.Fatalf("ListMRDeliveryStates: %v", err)
	}
	for _, row := range rows {
		if row.MRProject == ref && row.MRIID == mr.IID {
			t.Logf("delivery state: %s target=%s", row.State, row.TargetBranch)
			if row.State != "merged" {
				t.Fatalf("delivery state = %q, want merged", row.State)
			}
			return
		}
	}
	t.Fatal("delivery row for merged MR not found")
}
