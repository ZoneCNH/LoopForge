package repo

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow/internal/domain"

	"gorm.io/gorm"
)

// providerTokenID derives the row id from the credential scope so saving the
// same (kind, instance) again updates in place instead of duplicating.
func providerTokenID(providerKind, instanceKey string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(providerKind+"\x00"+instanceKey)))
}

func normalizeProviderTokenScope(providerKind, instanceKey string) (string, string, error) {
	kind := strings.ToLower(strings.TrimSpace(providerKind))
	if kind == "" {
		return "", "", errors.New("provider kind is required")
	}
	instance := strings.TrimSpace(instanceKey)
	if kind == domain.ProviderTokenGitLab {
		instance = domain.NormalizeGitLabInstanceKey(instance)
		if instance == "" {
			return "", "", errors.New("GitLab instance key must be an http(s) base URL")
		}
	}
	return kind, instance, nil
}

func tokenHint(token string) string {
	token = strings.TrimSpace(token)
	if len(token) <= 4 {
		return ""
	}
	return token[len(token)-4:]
}

// ProviderTokenDescriptions lists a user's provider credentials without
// ever selecting the secret column.
func (r *UsersRepo) ProviderTokenDescriptions(userID int64) ([]domain.ProviderToken, error) {
	rows := make([]providerTokenRow, 0)
	err := r.DB.Model(&providerTokenRow{}).
		Select("id", "provider_kind", "instance_key", "name", "credential_type", "token_hint", "scopes_summary", "expires_at", "last_validated_at", "status", "created_at", "updated_at").
		Where("user_id = ?", userID).Order("provider_kind, instance_key").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]domain.ProviderToken, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.ProviderToken{
			ID: row.ID, ProviderKind: row.ProviderKind, InstanceKey: row.InstanceKey,
			Name: row.Name, CredentialType: row.CredentialType, TokenHint: row.TokenHint,
			ScopesSummary: row.ScopesSummary, ExpiresAt: row.ExpiresAt,
			LastValidatedAt: row.LastValidatedAt, Status: row.Status,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}
	return out, nil
}

// SaveProviderToken creates or replaces the credential for
// (user, providerKind, instanceKey). The token is stored verbatim; callers
// must never log it.
func (r *UsersRepo) SaveProviderToken(userID int64, providerKind, instanceKey, credentialType, token, scopesSummary string, expiresAt *time.Time) (domain.ProviderToken, error) {
	kind, instance, err := normalizeProviderTokenScope(providerKind, instanceKey)
	if err != nil {
		return domain.ProviderToken{}, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return domain.ProviderToken{}, errors.New("token is required")
	}
	credentialType = strings.ToLower(strings.TrimSpace(credentialType))
	if credentialType == "" {
		credentialType = domain.ProviderCredentialPAT
	}
	switch credentialType {
	case domain.ProviderCredentialPAT, domain.ProviderCredentialProjectTok, domain.ProviderCredentialGroupToken:
	default:
		return domain.ProviderToken{}, fmt.Errorf("unsupported credential type %q", credentialType)
	}
	id := providerTokenID(kind, instance)
	now := time.Now().UTC()
	name := kind + " " + instance
	if runes := []rune(name); len(runes) > 80 {
		name = string(runes[:80])
	}
	row := providerTokenRow{
		UserID: userID, ID: id, ProviderKind: kind, InstanceKey: instance,
		Name: name, CredentialType: credentialType, Token: token,
		TokenHint: tokenHint(token), ScopesSummary: strings.TrimSpace(scopesSummary),
		ExpiresAt: expiresAt, Status: domain.ProviderTokenActive,
		CreatedAt: now, UpdatedAt: now,
	}
	// Preserve created_at on update; a replaced token starts active again and
	// the next validation run flips it to auth_failed if it still does not work.
	var existing providerTokenRow
	if err := r.DB.Select("created_at").Where("user_id = ? AND id = ?", userID, id).Take(&existing).Error; err == nil {
		row.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ProviderToken{}, err
	}
	if err := r.DB.Save(&row).Error; err != nil {
		return domain.ProviderToken{}, err
	}
	return domain.ProviderToken{
		ID: row.ID, ProviderKind: row.ProviderKind, InstanceKey: row.InstanceKey,
		Name: row.Name, CredentialType: row.CredentialType, TokenHint: row.TokenHint,
		ScopesSummary: row.ScopesSummary, ExpiresAt: row.ExpiresAt,
		Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// DeleteProviderToken removes one credential owned by userID.
func (r *UsersRepo) DeleteProviderToken(userID int64, id string) error {
	return r.DB.Where("user_id = ? AND id = ?", userID, id).Delete(&providerTokenRow{}).Error
}

// ProviderTokenSecret resolves the stored token for one scope. It is the
// only read path that returns the secret; callers must keep it in memory.
func (r *UsersRepo) ProviderTokenSecret(userID int64, providerKind, instanceKey string) (string, bool) {
	kind, instance, err := normalizeProviderTokenScope(providerKind, instanceKey)
	if err != nil {
		return "", false
	}
	var row providerTokenRow
	err = r.DB.Select("token").Where("user_id = ? AND provider_kind = ? AND instance_key = ? AND status != ?",
		userID, kind, instance, domain.ProviderTokenRevoked).Take(&row).Error
	if err != nil {
		return "", false
	}
	return row.Token, true
}

// MarkProviderTokenStatus records the outcome of a credential validation so
// the UI can surface AUTH_FAILED without another API call.
func (r *UsersRepo) MarkProviderTokenStatus(userID int64, providerKind, instanceKey, status string, validatedAt time.Time) error {
	kind, instance, err := normalizeProviderTokenScope(providerKind, instanceKey)
	if err != nil {
		return err
	}
	switch status {
	case domain.ProviderTokenActive, domain.ProviderTokenAuthFailed:
	default:
		return fmt.Errorf("unsupported token status %q", status)
	}
	return r.DB.Model(&providerTokenRow{}).
		Where("user_id = ? AND provider_kind = ? AND instance_key = ?", userID, kind, instance).
		Updates(map[string]any{"status": status, "last_validated_at": validatedAt.UTC()}).Error
}
