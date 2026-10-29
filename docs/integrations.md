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
