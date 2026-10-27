-- 0001: Meta key/value repo. Only what Meta needs; later modules add
-- their tables in their own migrations with their contract tests.
CREATE TABLE meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
