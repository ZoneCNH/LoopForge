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

// gitLabMergeStatus normalizes detailed_merge_status. Async states
// (checking/unchecked/preparing) map to "unknown" — never mergeable —
// so callers retry instead of merging blind.
func gitLabMergeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "mergeable":
		return "mergeable"
	case "checking", "unchecked", "preparing", "":
		return "unknown"
	default:
		return "blocked"
	}
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
		MergeStatus:  gitLabMergeStatus(mr.DetailedMergeStatus),
		CheckedAt:    checkedAt.UTC(),
	}
	if mr.MergedAt != nil {
		snap.MergedAt = mr.MergedAt.UTC()
	}
	return snap
}
