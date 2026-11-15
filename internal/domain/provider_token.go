package domain

import "time"

// Provider token kinds accepted by user_provider_tokens. provider_kind is
// the collaboration provider; instance_key scopes the credential to one
// self-managed deployment (empty for single-instance providers).
const (
	ProviderTokenGitLab = "gitlab"
)

// Provider token credential types.
const (
	ProviderCredentialPAT         = "pat"
	ProviderCredentialProjectTok  = "project_token"
	ProviderCredentialGroupToken  = "group_token"
)

// Provider token lifecycle states.
const (
	ProviderTokenActive     = "active"
	ProviderTokenAuthFailed = "auth_failed"
	ProviderTokenRevoked    = "revoked"
)

// ProviderToken is the public description of a per-provider credential.
// The secret itself is only readable through the repo secret path and is
// never serialized into API responses.
type ProviderToken struct {
	ID              string     `json:"id"`
	ProviderKind    string     `json:"providerKind"`
	InstanceKey     string     `json:"instanceKey"`
	Name            string     `json:"name"`
	CredentialType  string     `json:"credentialType"`
	TokenHint       string     `json:"tokenHint,omitempty"`
	ScopesSummary   string     `json:"scopesSummary,omitempty"`
	ExpiresAt       *time.Time `json:"expiresAt,omitempty"`
	LastValidatedAt *time.Time `json:"lastValidatedAt,omitempty"`
	Status          string     `json:"status"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}
