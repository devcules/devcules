-- 0010: projects remember whether their folder is a git repository,
-- so the tree can mark repos apart from plain folders. A cloned
-- project is always one; a picked folder is one when it holds .git.

ALTER TABLE projects ADD COLUMN git INTEGER NOT NULL DEFAULT 0;
UPDATE projects SET git = 1 WHERE repo != '';
