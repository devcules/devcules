-- 0009: projects. A project names one working directory on one
-- runner or machine; terminals and agents opened from the Projects
-- tab start there. Names are unique so the tab can address them.

CREATE TABLE projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL CHECK (kind IN ('runner', 'machine')),
  target TEXT NOT NULL,
  dir TEXT NOT NULL,
  repo TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);
