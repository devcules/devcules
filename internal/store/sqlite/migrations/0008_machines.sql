-- 0008: machines and their one-time enrolment codes. A code is
-- shown once (like a runner token); only its SHA-256 hash is stored
-- and serves in the DB. Enrol is single-use: claiming sets used_at
-- and creates the machine row atomically. Challenge never leaves memory.

CREATE TABLE enrol_codes (
  id TEXT PRIMARY KEY,
  code_hash BLOB NOT NULL UNIQUE,
  label TEXT NOT NULL,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE machines (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  pubkey BLOB NOT NULL,
  enrolled_at TEXT NOT NULL,
  last_seen TEXT NOT NULL DEFAULT ''
);
