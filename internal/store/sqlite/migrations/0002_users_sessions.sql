-- 0002: auth's repos. Users holds one password hash per username
-- (passwords never reach the store); sessions holds the SHA-256 of
-- each bearer token (tokens never reach the store). Times are UTC
-- RFC 3339 strings. Sessions reference their user so deleting a user
-- revokes its sessions.
CREATE TABLE users (
  username TEXT PRIMARY KEY,
  password_hash TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL
);
