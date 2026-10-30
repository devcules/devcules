-- 0011: model provider profiles. Each row is one endpoint the router
-- serves at /llm/<id>/...: its API format, base URL and default
-- model. Keys stay in the vault (provider/<id>/api_key). Serve seeds
-- Meta, Anthropic and OpenAI once, so keys saved before this keep
-- their provider.

CREATE TABLE providers (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  format TEXT NOT NULL CHECK (format IN ('openai', 'anthropic')),
  also_anthropic INTEGER NOT NULL DEFAULT 0,
  base_url TEXT NOT NULL,
  api TEXT NOT NULL DEFAULT 'auto' CHECK (api IN ('auto', 'chat', 'responses')),
  model TEXT NOT NULL DEFAULT '',
  is_default INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);

-- At most one default.
CREATE UNIQUE INDEX providers_one_default ON providers (is_default) WHERE is_default = 1;
