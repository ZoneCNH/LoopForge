-- Manual, idempotent MySQL migration for Multi-Git Integration (MGIT-004).
-- Per-user, per-provider-instance API credentials (GitLab self-managed
-- first). Secrets are stored verbatim in `token`; API responses only expose
-- token_hint. The (user_id, id) primary key mirrors user_git_ssh_keys; id is
-- a sha256 of provider_kind + instance_key so re-saving the same scope
-- updates in place.
CREATE TABLE IF NOT EXISTS user_provider_tokens (
  user_id BIGINT NOT NULL,
  id VARCHAR(64) NOT NULL,
  provider_kind VARCHAR(32) NOT NULL,
  instance_key TEXT NOT NULL,
  name VARCHAR(80) NOT NULL,
  credential_type VARCHAR(32) NOT NULL,
  token TEXT NOT NULL,
  token_hint VARCHAR(32) NOT NULL DEFAULT '',
  scopes_summary VARCHAR(255) NOT NULL DEFAULT '',
  expires_at DATETIME(6) NULL,
  last_validated_at DATETIME(6) NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (user_id, id),
  KEY idx_provider_tokens_scope (user_id, provider_kind, instance_key(191))
);
