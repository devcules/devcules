# Model router

Every model call an agent makes goes agent → serve's router → provider,
at `/llm/{provider}/...`. The agent never holds a provider key. It holds
a per-launch token that works only here, and the router:

1. checks the token (expired, revoked or meant for another provider is
   `401`/`403`);
2. redacts the JSON body under the live policy (`docs/redact.md`), or
   refuses it in `block` mode;
3. adds the real provider key from the vault (`provider/<id>/api_key`);
4. forwards the call and restores this exchange's placeholders in the
   reply, streamed replies included.

`internal/router` holds the router, `internal/launch` the tokens, and
`plugins/providers/meta` the Meta Model API provider. The router never
imports a provider; serve hands them in.

## Routes

| Method | Path under `/llm/{provider}/` | Style |
|---|---|---|
| `POST` | `v1/chat/completions` | OpenAI Chat Completions |
| `POST` | `v1/responses` | OpenAI Responses |
| `POST` | `v1/messages` | Anthropic Messages |
| `POST` | `v1/messages/count_tokens` | Anthropic token count |
| `GET` | `v1/models`, `v1/models/{id}` | model list |

Anything else is `404`: the router is a narrow door, not an open proxy.
Errors use the caller's API style: the OpenAI envelope
`{"error":{"message","type","code"}}` everywhere except
`v1/messages*`, which gets the Anthropic one
`{"type":"error","error":{"type","message"}}`.

Point an SDK at the router with the launch token as its key:

| SDK | Base URL | Key goes in |
|---|---|---|
| OpenAI | `http://serve:7659/llm/meta/v1` | `Authorization: Bearer` |
| Anthropic | `http://serve:7659/llm/meta` | `x-api-key` |

From the host, use `http://localhost:7659` instead of `serve:7659`.

## Launch tokens

A token is `dvl_` plus 32 random bytes in base64url (47 characters).
Only its SHA-256 reaches the store, and the token is shown once, when
minted. It lasts 12 hours by default (1 minute to 7 days), can be
limited to one provider, and can be revoked. Tokens that expired more
than a day ago are deleted when serve starts. M3.3 mints one per agent
launch; until then, mint by hand:

```sh
# needs a signed-in session cookie (jar)
curl -s -b jar -H 'Content-Type: application/json' \
  -d '{"label":"try it","provider":"meta","ttlMinutes":60}' \
  http://localhost:7659/api/launch-tokens
# → 201 {"token":"dvl_…","launch":{"id":…,"label":…,"expires":…}}
```

| Route | Does |
|---|---|
| `GET /api/launch-tokens` | Every token, newest first, without the token itself |
| `POST /api/launch-tokens` | `{label, provider, ttlMinutes}`; label required (100 bytes max), provider empty or a routed one, `ttlMinutes` 0 for the default. `201 {token, launch}`, `Cache-Control: no-store` |
| `DELETE /api/launch-tokens/{id}` | Revoke. `204`, or `404` for an unknown id |

All three need a session; the two writes also pass the cross-origin check. The
`/llm/` routes take no session: the launch token is their only
credential.

## What is redacted

Every string in the request JSON that carries prose: message content,
system prompts, tool descriptions and arguments, tool results. Keys
that are structure, not prose (`model`, `role`, `type`, ids,
`signature`, `encrypted_content`, `media_type` and the like), and
`data:` URIs are left as they are. An empty body (as on `GET`) is
forwarded empty; a body that is not one JSON value is refused, because
it cannot be scanned.

## Restoring replies

A plain JSON reply is restored as a whole. A streamed reply (SSE) is
restored per event, in the fields that carry text:

- Chat Completions chunks: `delta.content`, `delta.reasoning_content`
  and tool call `arguments`.
- Anthropic `content_block_delta`: `text`, `thinking` and
  `partial_json`.
- Responses `*.delta` events.

A placeholder split across two events is held back until it is whole
(at most 36 bytes), and any held tail is flushed as one more delta
before the event that closes the block. When the request issued no
placeholder, the reply is passed through untouched.

Only these upstream headers come back: `Content-Type`, `Retry-After`,
`Cache-Control`, the request ids, and the `x-ratelimit-*` /
`anthropic-ratelimit-*` hints. Only `Content-Type`, `Accept`,
`Anthropic-Version`, `Anthropic-Beta`, `OpenAI-Beta` and
`Idempotency-Key` go up; cookies and the launch token never do.

## Limits and timeouts

The request body may be 32 MiB and a non-streamed reply 64 MiB. The
body has a 2 minute read window; the reply has no write deadline, so
long streams are not cut by serve's server-wide timeouts.

## Traces

Every call that passes the token check is traced for its launch
(`docs/audit.md`): the model, the last user text of the redacted
request, the reply text and tool calls as the provider sent them, token
counts from `usage`, the status, the duration and the redaction counts.
The router reads the provider's own bytes for this, never the restored
reply, so a trace holds placeholders, not values. A blocked request is
traced with its counts and no text.

## Logs

One line per call, `llm call`, with the provider, path, status,
duration, `stream`, the launch id, and a `redacted_<type>` count per
type found. Never a body, a token or a key.

## Testing

- Unit tests run the router against a fake provider, including
  `FuzzChatStream` (placeholders split at random points of a stream).
- `devcules mockllm` is a hidden, loopback-only fake provider for the
  smoke test (`MOCKLLM_LISTEN`, default `127.0.0.1:7701`; it records
  what it received in `MOCKLLM_LOG`). `scripts/smoke.sh` points serve
  at it, mints a token, and checks that a fake key reaches the mock
  masked and comes back restored, streamed and not, and that a revoked
  token is refused.
- `plugins/providers/meta/live_test.go` calls the real API when
  `META_API_KEY` is set (`META_MODEL`, default `muse-spark-1.3`), and
  skips otherwise.
