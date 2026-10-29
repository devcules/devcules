-- 0007: approvals and Web Push. Launch tokens gain the runner they
-- run on (empty when not tied to one), which picks their access mode.
-- approvals.created_at uses a fixed-width UTC time so it sorts as
-- text; answered_at and answered_by are empty while pending. A push
-- subscription is keyed by its endpoint; p256dh and auth are the
-- browser's public key and secret, not credentials of ours.
ALTER TABLE launch_tokens ADD COLUMN runner TEXT NOT NULL DEFAULT '';

CREATE TABLE approvals (
  id TEXT PRIMARY KEY,
  launch_id TEXT NOT NULL,
  runner TEXT NOT NULL,
  agent TEXT NOT NULL,
  tool TEXT NOT NULL,
  command TEXT NOT NULL,
  rule TEXT NOT NULL,
  irreversible INTEGER NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  answered_at TEXT NOT NULL DEFAULT '',
  answered_by TEXT NOT NULL DEFAULT ''
);
CREATE INDEX approvals_status_created ON approvals (status, created_at);
CREATE INDEX approvals_created ON approvals (created_at);

CREATE TABLE push_subscriptions (
  endpoint TEXT PRIMARY KEY,
  p256dh BLOB NOT NULL,
  auth BLOB NOT NULL,
  label TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_ok_at TEXT NOT NULL DEFAULT ''
);
