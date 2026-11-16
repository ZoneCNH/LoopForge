package tracker

import (
	"strings"
	"time"
)

// gitLabMRState normalizes a GitLab MR state. GitLab has no "reopened"
// state: reopening returns the MR to "opened".
func gitLabMRState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "opened":
		return "opened"
	case "merged":
		return "merged"
	case "closed":
		return "closed"
	default:
		return "unknown"
	}
}

// gitLabMergeStatus normalizes detailed_merge_status / legacy merge_status.
// Async and recheck states map to "unknown" — never mergeable — so callers
// retry instead of merging blind; deterministic refusals map to "blocked".
func gitLabMergeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "mergeable", "can_be_merged":
		return "mergeable"
	case "checking", "unchecked", "preparing", "cannot_be_merged_recheck", "ci_still_running", "":
		return "unknown"
	default:
		return "blocked"
	}
}

// gitLabEffectiveMergeStatus prefers detailed_merge_status (GitLab ≥15.6)
// and falls back to the legacy merge_status field on older instances that do
// not return the detailed value at all.
func gitLabEffectiveMergeStatus(mr gitlabMergeRequest) string {
	if detailed := strings.TrimSpace(mr.DetailedMergeStatus); detailed != "" {
		return gitLabMergeStatus(detailed)
	}
	return gitLabMergeStatus(mr.MergeStatus)
}

// gitLabPipelineState normalizes a pipeline status.
func gitLabPipelineState(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created", "waiting_for_resource", "preparing", "pending":
		return "pending"
	case "running":
		return "running"
	case "success":
		return "passed"
	case "failed":
		return "failed"
	case "canceled":
		return "canceled"
	case "skipped", "manual":
		return "skipped"
	default:
		return "unknown"
	}
}

// gitLabIssueState maps a GitLab issue state onto the platform issue
// status vocabulary, mirroring gongfengStateToStatus.
func gitLabIssueState(state string) (status, externalState, closeSource string) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "opened":
		return "todo", "opened", ""
	case "closed":
		return "closed", "closed", "external"
	default:
		return "backlog", strings.ToLower(strings.TrimSpace(state)), ""
	}
}

func gitLabMRSnapshot(mr gitlabMergeRequest, checkedAt time.Time) MRStateSnapshot {
	snap := MRStateSnapshot{
		State:        gitLabMRState(mr.State),
		RawState:     strings.ToLower(strings.TrimSpace(mr.State)),
		TargetBranch: strings.TrimSpace(mr.TargetBranch),
		MergeStatus:  gitLabEffectiveMergeStatus(mr),
		CheckedAt:    checkedAt.UTC(),
	}
	if mr.MergedAt != nil {
		snap.MergedAt = mr.MergedAt.UTC()
	}
	return snap
}
