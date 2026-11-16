package tracker

import (
	"errors"
	"strings"
	"time"

	"agentflow/internal/domain"
)

// Provider kinds understood by the collaboration dispatch. "gongfeng" and
// "tapd" stay on the legacy MCP path and do not implement
// CollaborationProvider.
const (
	ProviderGitHub = "github"
	ProviderGitLab = domain.GitLabProviderKind
)

// Sentinel errors classified from provider HTTP responses. Callers map them
// to sync-log status and connection health; messages must never contain
// tokens or response bodies.
var (
	ErrCapabilityUnsupported = errors.New("provider capability unsupported")
	ErrProviderAuth          = errors.New("provider authentication failed")
	ErrProviderDenied        = errors.New("provider permission denied")
	ErrProviderNotFound      = errors.New("external object not found")
	ErrProviderConflict      = errors.New("external object conflict")
	ErrProviderRateLimited   = errors.New("provider rate limited")
	ErrProviderUnavailable   = errors.New("provider unavailable")
)

// ExternalRepoRef identifies an external repository instance for provider
// calls. It is derived from domain.Repository fields, never stored.
type ExternalRepoRef struct {
	Kind        string // ProviderGitHub | ProviderGitLab
	InstanceKey string // gitlab: normalized base URL; github: ""
	ExternalID  string // gitlab: numeric project id; github: "owner/repo"
	FullPath    string // gitlab: group/sub/repo; github: owner/repo
	WebURL      string
}

// MRStateSnapshot is the authoritative provider-side state of one change
// request, used by the polling reconciler.
type MRStateSnapshot struct {
	State        string // normalized: opened | merged | closed | reopened | unknown
	RawState     string
	TargetBranch string
	MergeStatus  string // normalized: mergeable | blocked | unknown
	MergedAt     time.Time
	CheckedAt    time.Time
}

// ProviderCapabilities declares what a provider instance can actually do.
// Values are runtime facts, not kind-based assumptions.
type ProviderCapabilities struct {
	Issues         bool `json:"issues"`
	ChangeRequests bool `json:"changeRequests"`
	Merge          bool `json:"merge"`
	Approvals      bool `json:"approvals"`
	Pipelines      bool `json:"pipelines"`
	Webhooks       bool `json:"webhooks"`
	OAuth          bool `json:"oauth"`
}

// providerTokenStore is the optional credential surface the tracker needs.
// *repo.Store satisfies it through the embedded UsersRepo; stores without
// provider tokens simply report no credential.
type providerTokenStore interface {
	ProviderTokenSecret(userID int64, providerKind, instanceKey string) (string, bool)
}

// projectProviderKind classifies a GongfengProjectID identity string.
// It only looks at the prefix; payload validation stays with the provider.
func projectProviderKind(project string) string {
	project = strings.TrimSpace(project)
	switch {
	case strings.HasPrefix(project, "github:"):
		return ProviderGitHub
	case domain.IsGitLabProjectRef(project):
		return ProviderGitLab
	default:
		return ""
	}
}

// isGitHubProject / isGitLabProject are the dispatch predicates used across
// tracker paths that must not treat gitlab: identities as gongfeng projects.
func isGitHubProject(project string) bool {
	return strings.HasPrefix(strings.TrimSpace(project), "github:")
}

func isGitLabProject(project string) bool {
	return domain.IsGitLabProjectRef(project)
}

// gitLabRefFor resolves the ExternalRepoRef for a gitlab: project identity.
func gitLabRefFor(project string) (ExternalRepoRef, bool) {
	instanceKey, fullPath, ok := domain.ParseGitLabProjectRef(project)
	if !ok {
		return ExternalRepoRef{}, false
	}
	ref := ExternalRepoRef{
		Kind:        ProviderGitLab,
		InstanceKey: instanceKey,
		FullPath:    fullPath,
		WebURL:      domain.GitLabWebURL(instanceKey, fullPath),
	}
	return ref, true
}

// resolveProviderToken returns the actor's provider token for a GitLab
// instance, falling back to the project owner — mirroring how githubClient
// resolves user.GitToken.
func (s *Service) resolveProviderToken(project string, ref ExternalRepoRef) (string, error) {
	store, ok := s.projectAuth.(providerTokenStore)
	if !ok || store == nil {
		return "", ErrProviderAuth
	}
	if s.projectAuth != nil {
		for _, repo := range s.projectAuth.Repositories() {
			if repo.Provider != ProviderGitLab || repo.GongfengProjectID != project {
				continue
			}
			if p, found := s.projectAuth.Project(repo.ProjectID); found && p.OwnerID > 0 {
				if token, hit := store.ProviderTokenSecret(p.OwnerID, ProviderGitLab, ref.InstanceKey); hit {
					return token, nil
				}
			}
			break
		}
	}
	return "", ErrProviderAuth
}

