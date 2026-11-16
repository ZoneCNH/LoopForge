package tracker

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"agentflow/internal/domain"
)

// gitlabClientForProject resolves the REST client for a gitlab: project
// identity. The token comes from the project owner's user_provider_tokens
// entry for the instance (see resolveProviderToken).
func (s *Service) gitlabClientForProject(project string) (*gitlabClient, ExternalRepoRef, error) {
	ref, ok := gitLabRefFor(project)
	if !ok {
		return nil, ExternalRepoRef{}, fmt.Errorf("invalid GitLab repository identity")
	}
	token, err := s.resolveProviderToken(project, ref)
	if err != nil {
		return nil, ref, err
	}
	return newGitLabClient(ref.InstanceKey, token), ref, nil
}

// gitlabPipelineForSHA returns the newest pipeline for a commit SHA.
// Pipeline lookup is best-effort: any error yields empty status so delivery
// resolution never fails because CI metadata is unavailable.
func gitlabPipelineForSHA(client *gitlabClient, projectPath, sha string) (status, webURL string) {
	sha = strings.TrimSpace(sha)
	if client == nil || sha == "" {
		return "", ""
	}
	var items []gitlabPipeline
	if err := client.request("GET", projectPath, "/pipelines", url.Values{
		"sha":      {sha},
		"per_page": {"1"},
	}, nil, &items); err != nil || len(items) == 0 {
		return "", ""
	}
	return gitLabPipelineState(items[0].Status), strings.TrimSpace(items[0].WebURL)
}

// gitlabDelivery converts a GitLab MR into the platform DeliveryMR shape.
func gitlabDelivery(ref ExternalRepoRef, mr gitlabMergeRequest) domain.DeliveryMR {
	state := gitLabMRState(mr.State)
	d := domain.DeliveryMR{
		Project:       ref.ProjectID(),
		IID:           mr.IID,
		URL:           strings.TrimSpace(mr.WebURL),
		State:         state,
		GongfengState: state,
		TargetBranch:  strings.TrimSpace(mr.TargetBranch),
	}
	if d.URL == "" {
		d.URL = domain.GitLabWebURL(ref.InstanceKey, ref.FullPath) + "/-/merge_requests/" + strconv.Itoa(mr.IID)
	}
	return d
}

// ProjectID returns the canonical gitlab: identity string for this ref.
func (r ExternalRepoRef) ProjectID() string {
	return domain.FormatGitLabProjectRef(r.InstanceKey, r.FullPath)
}

// gitlabFindBranch returns the newest non-closed MR whose source branch is
// sourceBranch, mirroring githubFindBranch/pickBranchMR semantics.
func (s *Service) gitlabFindBranch(project, sourceBranch string) (domain.DeliveryMR, bool, error) {
	client, ref, err := s.gitlabClientForProject(project)
	if err != nil {
		return domain.DeliveryMR{}, false, err
	}
	src := strings.TrimSpace(sourceBranch)
	if src == "" {
		return domain.DeliveryMR{}, false, nil
	}
	var best, bestClosed *gitlabMergeRequest
	err = client.listPages(ref.FullPath, "/merge_requests", url.Values{
		"state":         {"all"},
		"source_branch": {src},
		"order_by":      {"updated_at"},
		"sort":          {"desc"},
	}, func(raw json.RawMessage) (bool, error) {
		var items []gitlabMergeRequest
		if err := json.Unmarshal(raw, &items); err != nil {
			return false, err
		}
		for i := range items {
			item := items[i]
			if !domain.SameGitBranch(strings.TrimSpace(item.SourceBranch), src) {
				continue
			}
			state := gitLabMRState(item.State)
			if state == "closed" {
				if bestClosed == nil || item.IID > bestClosed.IID {
					cp := item
					bestClosed = &cp
				}
				continue
			}
			if best == nil || item.IID > best.IID {
				cp := item
				best = &cp
			}
		}
		// 只要还没见到非 closed MR 就继续翻页：closed 只作兜底，
		// 更新的 closed 不能挡住更旧的 opened/reopened。
		return best == nil, nil
	})
	if err != nil {
		return domain.DeliveryMR{}, false, err
	}
	// 与 githubFindBranch 一致：优先非 closed 的 MR，仅有 closed 时返回最新 closed。
	picked := best
	if picked == nil {
		picked = bestClosed
	}
	if picked == nil {
		return domain.DeliveryMR{}, false, nil
	}
	d := gitlabDelivery(ref, *picked)
	d.PipelineStatus, d.PipelineURL = gitlabPipelineForSHA(client, ref.FullPath, picked.SHA)
	return d, true, nil
}

// gitlabMergeBranch finds or creates the MR source→target and merges it.
// Merge is refused unless detailed_merge_status reports mergeable; async
// states surface as ErrProviderConflict so callers retry.
func (s *Service) gitlabMergeBranch(project, sourceBranch, targetBranch, title, description string) (string, error) {
	src := strings.TrimSpace(sourceBranch)
	if src == "" {
		return "", fmt.Errorf("source branch is required")
	}
	client, ref, err := s.gitlabClientForProject(project)
	if err != nil {
		return "", err
	}

	// Find an existing MR for the source branch.
	var mr *gitlabMergeRequest
	err = client.listPages(ref.FullPath, "/merge_requests", url.Values{
		"state":         {"opened"},
		"source_branch": {src},
		"per_page":      {"20"},
	}, func(raw json.RawMessage) (bool, error) {
		var items []gitlabMergeRequest
		if err := json.Unmarshal(raw, &items); err != nil {
			return false, err
		}
		for i := range items {
			item := items[i]
			if !domain.SameGitBranch(strings.TrimSpace(item.SourceBranch), src) {
				continue
			}
			if tgt := strings.TrimSpace(targetBranch); tgt != "" && !strings.EqualFold(strings.TrimSpace(item.TargetBranch), tgt) {
				continue
			}
			cp := item
			mr = &cp
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return "", err
	}

	if mr == nil {
		tgt := strings.TrimSpace(targetBranch)
		if tgt == "" {
			return "", ErrNoOpenMR
		}
		if title == "" {
			title = "LoopForge: " + src + " → " + tgt
		}
		input := map[string]any{
			"source_branch": src,
			"target_branch": tgt,
			"title":         title,
		}
		if description != "" {
			input["description"] = description
		}
		var created gitlabMergeRequest
		if err := client.request("POST", ref.FullPath, "/merge_requests", nil, input, &created); err != nil {
			return "", err
		}
		mr = &created
	} else if gitLabMRState(mr.State) == "merged" {
		return "!" + strconv.Itoa(mr.IID), nil
	}

	// Re-fetch authoritative merge status before merging.
	var fresh gitlabMergeRequest
	if err := client.request("GET", ref.FullPath, "/merge_requests/"+strconv.Itoa(mr.IID), nil, nil, &fresh); err != nil {
		return "", err
	}
	mr = &fresh
	switch gitLabMRState(mr.State) {
	case "merged":
		return "!" + strconv.Itoa(mr.IID), nil
	case "closed":
		return "", fmt.Errorf("GitLab MR !%d is closed", mr.IID)
	}
	switch gitLabEffectiveMergeStatus(*mr) {
	case "mergeable":
	case "unknown":
		return "", fmt.Errorf("%w: merge request !%d merge status is %q", ErrProviderConflict, mr.IID, mr.DetailedMergeStatus)
	default:
		return "", fmt.Errorf("%w: merge request !%d cannot merge (status %q)", ErrProviderDenied, mr.IID, mr.DetailedMergeStatus)
	}

	mergeBody := map[string]any{"should_remove_source_branch": false}
	if sha := strings.TrimSpace(mr.SHA); sha != "" {
		mergeBody["sha"] = sha
	}
	var result struct {
		State string `json:"state"`
	}
	if err := client.request("PUT", ref.FullPath, "/merge_requests/"+strconv.Itoa(mr.IID)+"/merge", nil, mergeBody, &result); err != nil {
		return "", fmt.Errorf("merge MR !%d: %w", mr.IID, err)
	}
	if gitLabMRState(result.State) != "merged" {
		return "", fmt.Errorf("%w: merge request !%d returned state %q", ErrProviderConflict, mr.IID, result.State)
	}
	return "!" + strconv.Itoa(mr.IID), nil
}

// RefreshGitLabDelivery re-reads one GitLab MR from the provider API and
// persists the normalized delivery state. It is the webhook/polling entry
// point; callers never trust webhook payload state directly.
func (s *Service) RefreshGitLabDelivery(store MRDeliveryStore, project string, iid int) error {
	return s.gitlabRefreshDelivery(store, project, iid)
}

// gitlabRefreshDelivery polls one MR's authoritative state into
// mr_delivery_states, mirroring refreshGitHubDelivery.
func (s *Service) gitlabRefreshDelivery(store MRDeliveryStore, project string, iid int) error {
	client, ref, err := s.gitlabClientForProject(project)
	if err != nil {
		return err
	}
	var mr gitlabMergeRequest
	if err := client.request("GET", ref.FullPath, "/merge_requests/"+strconv.Itoa(iid), nil, nil, &mr); err != nil {
		return err
	}
	snap := gitLabMRSnapshot(mr, time.Now().UTC())
	mergedAt := snap.MergedAt
	return store.UpdateMRDeliveryState(project, iid, snap.State, snap.TargetBranch, mergedAt, snap.CheckedAt)
}

// pullGitLabIssues imports GitLab issues for one repository, mirroring
// pullGitHubIssues: skip MR-adjacent noise is unnecessary (GitLab issues
// API excludes MRs), map state, and preserve local clarifications.
func (s *Service) pullGitLabIssues(store IssueStoreAdapter, repo domain.Repository) (int, error) {
	client, ref, err := s.gitlabClientForProject(repo.GongfengProjectID)
	if err != nil {
		return 0, err
	}
	count := 0
	err = client.listPages(ref.FullPath, "/issues", url.Values{"state": {"all"}}, func(raw json.RawMessage) (bool, error) {
		var items []gitlabIssue
		if err := json.Unmarshal(raw, &items); err != nil {
			return false, err
		}
		for _, item := range items {
			status, externalState, closeSource := gitLabIssueState(item.State)
			id := fmt.Sprintf("gitlab-%s-%d", repo.ID, item.IID)
			issue := domain.Issue{
				ID: id, Number: item.IID, ProjectID: repo.ProjectID, RepositoryID: repo.ID,
				BaseBranch: repo.DefaultBranch, Kind: "issue", Title: item.Title,
				Description: item.Desc, Priority: "medium", Source: "gitlab",
				Status: status, CloseSource: closeSource,
				CreatedAt: item.CreatedAt, UpdatedAt: time.Now().UTC(),
				ExternalRef: &domain.ExternalRef{
					Source: "gitlab", ExternalID: strconv.FormatInt(item.ID, 10),
					ExternalIID: item.IID, ExternalURL: item.WebURL,
					ExternalState: externalState, ExternalType: "issue",
					SyncedAt: time.Now().UTC(),
				},
			}
			issue.ExternalRef.SourceContentHash = githubBodyHash(item.Desc)
			if old, ok := store.Issue(id); ok {
				applyExistingIssue(&issue, old)
				if old.Clarification.Status != "" || (old.ExternalRef != nil && githubBodyHash(old.Description) != old.ExternalRef.SourceContentHash) {
					issue.Description = old.Description
				}
			}
			if err := store.PutIssue(issue); err != nil {
				return false, err
			}
			count++
		}
		return true, nil
	})
	return count, err
}
