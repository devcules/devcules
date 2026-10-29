-- 0005: per-launch tokens for the model router. token_hash is the
-- SHA-256 of the token; the token itself is never stored. provider is
-- empty when the token works for any provider. revoked_at and
-- last_used_at are empty strings until set. Times are UTC RFC 3339
-- strings.
CREATE TABLE launch_tokens (
  id TEXT PRIMARY KEY,
  token_hash BLOB NOT NULL UNIQUE,
  label TEXT NOT NULL,
  provider TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  revoked_at TEXT NOT NULL DEFAULT '',
  last_used_at TEXT NOT NULL DEFAULT ''
);
