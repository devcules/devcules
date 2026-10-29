-- 0006: the audit log and model call traces.
--
-- audit_log is append-only: the triggers below refuse every UPDATE and
-- DELETE, so even a bug or a stray SQL shell cannot rewrite history
-- through this database. seq counts from 1 with no gaps; hash chains
-- each entry to the one before (internal/audit computes and verifies
-- it). detail is a JSON object of short strings. Times are UTC RFC 3339
-- with nanoseconds, exactly as hashed.
CREATE TABLE audit_log (
  seq INTEGER PRIMARY KEY,
  at TEXT NOT NULL,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL,
  ip TEXT NOT NULL,
  detail TEXT NOT NULL,
  prev_hash BLOB NOT NULL,
  hash BLOB NOT NULL
);

CREATE TRIGGER audit_log_no_update BEFORE UPDATE ON audit_log
BEGIN
  SELECT RAISE(ABORT, 'audit_log is append-only');
END;

CREATE TRIGGER audit_log_no_delete BEFORE DELETE ON audit_log
BEGIN
  SELECT RAISE(ABORT, 'audit_log is append-only');
END;

-- trace_calls holds one row per model call through the router, as the
-- provider saw it: question, reply and tool_calls carry placeholders,
-- never redacted values. redacted is a JSON object of counts per type,
-- redacted_total their sum. tool_calls is a JSON array of {name, args}.
CREATE TABLE trace_calls (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  launch_id TEXT NOT NULL,
  at TEXT NOT NULL,
  provider TEXT NOT NULL,
  path TEXT NOT NULL,
  model TEXT NOT NULL,
  status INTEGER NOT NULL,
  stream INTEGER NOT NULL,
  duration_ms INTEGER NOT NULL,
  input_tokens INTEGER NOT NULL,
  output_tokens INTEGER NOT NULL,
  redacted TEXT NOT NULL,
  redacted_total INTEGER NOT NULL,
  question TEXT NOT NULL,
  reply TEXT NOT NULL,
  tool_calls TEXT NOT NULL
);

CREATE INDEX trace_calls_launch ON trace_calls (launch_id, id);
CREATE INDEX trace_calls_at ON trace_calls (at);
