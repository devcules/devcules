# Model provider profiles

A provider profile is one model endpoint the router serves at
`/llm/<id>/...`: a name, the API format it speaks, a base URL, which
OpenAI API agents use on it, and an optional default model. The key is
a vault secret (`provider/<id>/api_key`, `docs/vault.md`), never a
column. Profiles live in the `providers` table (migration 0011) and
`internal/providers` owns them; the router asks that package for the
provider on every call, so a profile added or edited in Settings serves
at once, without a restart.

| Field | Values | Notes |
|---|---|---|
| `id` | 1-32 lowercase letters and digits, starting with a letter | From the name when adding (`Local vLLM` → `localvllm`, then `localvllm2`). Fixed after that: launch tokens and the vault name use it. |
| `name` | 1-64 characters | What the cards and the new-terminal form show. |
| `format` | `openai`, `anthropic` | OpenAI-compatible: `Authorization: Bearer <key>`, `v1/chat/completions`, `v1/responses`, `v1/models`. Anthropic: `x-api-key: <key>` plus `anthropic-version` (the client's own is kept), `v1/messages`. |
| `alsoAnthropic` | bool | An OpenAI-format endpoint that serves `v1/messages` too, with the same bearer key. Meta is one. |
| `baseUrl` | URL | As typed, trimmed of spaces and trailing slashes. A trailing `/v1` is fine: the router drops it and appends `v1/...` itself. |
| `api` | `auto`, `chat`, `responses` | Which OpenAI API agents may use. `chat` means the endpoint has no `v1/responses`, so Codex is not offered it. Anthropic profiles keep `auto`. |
| `model` | model id, optional | The model a launch uses when the form leaves it blank. |
| `default` | one profile | Listed first after an agent's own provider. Moved with `PUT /api/providers/{id}/default`. |

## Built-ins

The first start with the table writes three profiles, once:

| id | Format | Base URL |
|---|---|---|
| `meta` (default) | OpenAI, also Anthropic | `DEVCULES_META_BASE_URL`, else `https://api.meta.ai` |
| `anthropic` | Anthropic | `DEVCULES_ANTHROPIC_BASE_URL`, else `https://api.anthropic.com` |
| `openai` | OpenAI | `DEVCULES_OPENAI_BASE_URL`, else `https://api.openai.com` |

Keys saved under those ids before profiles existed keep working. A
built-in the owner removes stays removed. When one of those variables
is set to another address than the vendor's, serve moves that built-in
there on every start (a test fake, a regional endpoint): the operator's
setting beats an edit in Settings.

## Base URL rules

A base URL must be:

- `https://`, or plain `http://` only for `localhost`, `*.localhost`,
  a loopback address or a private LAN address (`10/8`, `172.16/12`,
  `192.168/16`, `fc00::/7`), for a local vLLM or Ollama;
- free of a user or password (the key goes in the key field), a `?`
  query and a `#` fragment;
- never an unspecified, multicast or link-local address: `169.254.x.x`
  is where cloud metadata services answer.

The router never follows a redirect, so a key header cannot travel to
an address the owner did not save. A host name is not resolved when it
is checked: an https name that points at a private address is the
owner's choice to make.

## Agents

Each adapter names the API its CLI speaks (`Agent.Wire()`) and, if it
is limited to some providers, which (`Agent.Only()`). Serve offers an
agent every profile that serves its API, its own provider first, then
the default, then the rest by name:

| Agent | Wire | Offered |
|---|---|---|
| Claude Code | `messages` | Anthropic-format profiles, and profiles marked `alsoAnthropic` (Meta) |
| Codex | `responses` | OpenAI-format profiles whose `api` is `auto` or `responses`; Codex calls `/v1/responses` only |
| Muse Code | `responses` | `meta` only |

A model is required off the agent's own provider unless the profile
has a default model: another vendor has no model by the agent's
default name. `GET /api/agents` fills each agent's `providers` with
what it is offered now.

## API

Session for every route; session plus CSRF for every write, all
audited without the key.

| Route | Does | Audit |
|---|---|---|
| `GET /api/providers` | Every profile: all fields but the key, and `ready` (a key is saved). | |
| `POST /api/providers {name, format, baseUrl, api?, model?, alsoAnthropic?, default?, key?}` | Adds one: `201 {provider}`. The first profile is the default. | `provider.add` |
| `PUT /api/providers/{id} {…same, key?}` | Edits one. A blank key keeps the saved one, but only while the base URL stays on the same host: moving it elsewhere needs the key pasted again (`400`). `default: true` also makes it the default. | `provider.edit` |
| `DELETE /api/providers/{id}` | Removes it and its key. `409` for the default or the only profile. | `provider.remove` |
| `PUT /api/providers/{id}/default` | Makes it the default. | `provider.default` |
| `GET /api/providers/{id}/models` | Its model ids, read by serve with the vault key, cached five minutes. | |
| `PUT` / `DELETE /api/providers/{id}/key` | Sets or removes its key (`docs/vault.md`). | `provider.key.set` / `.remove` |
| `POST /api/providers/probe {format, baseUrl, key? , id?}` | "Load models" for a form not saved yet: the endpoint's `GET v1/models` with the typed key, or with profile `id`'s saved key when the host is the saved one. Ten seconds, nothing cached, the key never echoed. | |

## Testing

`internal/providers` tests the rules, seeding, pinning and the
cache; `internal/api/profiles_test.go` every route, the host rule for
saved keys and the probe; `internal/router` per-call resolution, the
probe and refused redirects; `plugins/agents/agenttest` that every
adapter names a known wire and routes to any profile its `Only` allows.
