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
	var best *gitlabMergeRequest
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
				continue
			}
			if best == nil || item.IID > best.IID {
				cp := item
				best = &cp
			}
		}
		return best == nil, nil // keep paging while nothing found
	})
	if err != nil {
		return domain.DeliveryMR{}, false, err
	}
	if best == nil {
		return domain.DeliveryMR{}, false, nil
	}
	return gitlabDelivery(ref, *best), true, nil
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
			if targetBranch != "" && !strings.EqualFold(strings.TrimSpace(item.TargetBranch), targetBranch) {
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
	switch gitLabMergeStatus(mr.DetailedMergeStatus) {
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
	return "!" + strconv.Itoa(mr.IID), nil
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
