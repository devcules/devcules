# Integrations

Facts about outside APIs that Devcules depends on, each checked against
the official docs on the date shown (PLAN.md rule 7). When one changes,
fix it here and in the code together.

## Meta Model API

Checked 2026-10-29 at <https://dev.meta.ai/docs>. Used by
`plugins/providers/meta` through the router (`docs/router.md`).

| Fact | Value |
|---|---|
| Base URL | `https://api.meta.ai` (`DEVCULES_META_BASE_URL` overrides it) |
| Auth | `Authorization: Bearer <key>`; keys look like `LLM_<id>_<secret>` |
| OpenAI-style | `POST /v1/chat/completions`, `POST /v1/responses`, `GET /v1/models` (optional `?client=`) |
| Anthropic-style | `POST /v1/messages`, `POST /v1/messages/count_tokens` |
| Chat models | `muse-spark-1.3` (the docs' default), `muse-spark-1.2`, `muse-spark-1.1`, `muse-spark-1.3-contributor`, `muse-spark-1.2-contributor`, `muse-wonder-1.0` (preview) |
| Other models | `sam-3.1` (segmentation, M4.4), `muse-image-1.0` |
| Streaming | SSE `data:` lines ending in `data: [DONE]`; `chat.completion.chunk` objects on the OpenAI side, Anthropic event names (`message_start`, `content_block_delta`, …) on the Messages side |
| Errors | OpenAI envelope `{"error":{"message","type","param","code"}}` |
| Rate limits | `x-ratelimit-*` headers; `Retry-After` on `429` and `503` |

The router forwards both API styles unchanged apart from redaction, so
an OpenAI SDK and an Anthropic SDK can both use the same key.

## Model Context Protocol

Checked 2026-10-29 at
<https://modelcontextprotocol.io/specification/2026-07-28> and
<https://modelcontextprotocol.io/specification/2025-11-25>. Used by
`internal/mcp` (`docs/mcp.md`). Tested against the official Python SDK
(`mcp` 2.3.0) in its `auto`, `2026-07-28` and `legacy` modes.

| Fact | Value |
|---|---|
| Transport | Streamable HTTP: one JSON-RPC message per `POST`; the answer is `application/json` or an SSE stream (Devcules always answers JSON) |
| `2026-07-28` | Stateless: no `initialize`, no sessions, no `ping`. Every request has `params._meta` keys `io.modelcontextprotocol/protocolVersion` and `io.modelcontextprotocol/clientCapabilities` (required) and `io.modelcontextprotocol/clientInfo` |
| `2026-07-28` headers | `MCP-Protocol-Version`, `Mcp-Method`, and `Mcp-Name` for `tools/call`; a mismatch with the body is `400` with `-32020` |
| `2026-07-28` errors | Unsupported version: `400`, `-32022`, `data: {supported, requested}`. Unknown method: `404`, `-32601`. Missing `_meta`: `400`, `-32602` |
| `2026-07-28` results | `resultType: "complete"`; `server/discover` returns `supportedVersions`, `capabilities`, `instructions`, `ttlMs`, `cacheScope`; `tools/list` carries `ttlMs` and `cacheScope` |
| `2025-11-25` handshake | `initialize` with `protocolVersion`, `capabilities`, `clientInfo`; the server answers the same version when supported, else its latest. `Mcp-Session-Id` is optional. A missing `MCP-Protocol-Version` header means `2025-03-26` |
| Security | Servers must validate `Origin` (DNS rebinding) and answer `403` to a bad one |
| Tools | Names 1 to 128 of `A-Z a-z 0-9 _ - .`; tool failures are results with `isError: true`; an unknown tool is `-32602` |
| Pagination | Opaque `cursor` / `nextCursor`; a bad cursor is `-32602` |
