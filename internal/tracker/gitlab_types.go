package tracker

import "time"

// GitLab REST v4 DTOs. Only fields the platform consumes are modeled; the
// rest of the payload is ignored.

type gitlabProject struct {
	ID            int64  `json:"id"`
	PathWithNS    string `json:"path_with_namespace"`
	WebURL        string `json:"web_url"`
	SSHURLToRepo  string `json:"ssh_url_to_repo"`
	HTTPURLToRepo string `json:"http_url_to_repo"`
	DefaultBranch string `json:"default_branch"`
}

type gitlabIssue struct {
	ID        int64     `json:"id"`
	IID       int       `json:"iid"`
	Title     string    `json:"title"`
	Desc      string    `json:"description"`
	State     string    `json:"state"`
	WebURL    string    `json:"web_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type gitlabMergeRequest struct {
	ID              int64      `json:"id"`
	IID             int        `json:"iid"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	State           string     `json:"state"`
	Draft           bool       `json:"draft"`
	WorkInProgress  bool       `json:"work_in_progress"`
	SourceBranch    string     `json:"source_branch"`
	TargetBranch    string     `json:"target_branch"`
	SHA             string     `json:"sha"`
	MergeStatus     string     `json:"merge_status"`          // legacy: can_be_merged etc.
	DetailedMergeStatus string `json:"detailed_merge_status"` // checking/mergeable/...
	WebURL          string     `json:"web_url"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	MergedAt        *time.Time `json:"merged_at"`
	ClosedAt        *time.Time `json:"closed_at"`
}

type gitlabPipeline struct {
	ID        int       `json:"id"`
	Status    string    `json:"status"`
	Ref       string    `json:"ref"`
	SHA       string    `json:"sha"`
	WebURL    string    `json:"web_url"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
