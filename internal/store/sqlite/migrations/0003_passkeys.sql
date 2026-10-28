-- 0003: auth's passkey repos. passkeys holds one row per registered
-- credential: the credential id, its COSE public key, the sign count,
-- the authenticator's AAGUID, transports, backup flags and the
-- user-facing name. users.webauthn_handle is the opaque user handle
-- minted on first registration; it is never the username. Times are
-- UTC RFC 3339 strings. Passkeys reference their user so deleting
-- a user revokes its passkeys, as with sessions.
ALTER TABLE users ADD COLUMN webauthn_handle BLOB;
CREATE TABLE passkeys (
  credential_id BLOB PRIMARY KEY,
  username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
  public_key BLOB NOT NULL,
  sign_count INTEGER NOT NULL,
  aaguid BLOB NOT NULL,
  transports TEXT NOT NULL,
  backup_eligible INTEGER NOT NULL,
  backup_state INTEGER NOT NULL,
  name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_used_at TEXT NOT NULL
);
CREATE INDEX passkeys_by_user ON passkeys (username);
