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
| SAM endpoint | `POST /v1/responses` with `model: "sam-3.1"`, one user message holding an `input_text` noun phrase and one `input_image` (URL or data URL). Non-streaming returns the full answer; `metadata.mask_encoding: "one_bit"` requests the smallest masks |
| SAM prompts | A short noun phrase, one concept per request ("green button", never "segment the bus" or "a or b"); zero matches completes with an empty output lane — a valid result |
| SAM output | Special-token text in the `output_text` lane: `<0f>` frame marker, then comma-separated records `id<|box;x1;y1;x2;y2;w;h|><|mask;x=0;y=0;data=H,W,payload|>`. Boxes are source pixels; masks are base85 (0x21..0x7e minus `",;<>|\`), length-prefixed, range-coded (`~` lossless coverage values >=129 in-mask, `!` one_bit raster). Verified against the official parser's own encoder output |
| Streaming | SSE `data:` lines ending in `data: [DONE]`; `chat.completion.chunk` objects on the OpenAI side, Anthropic event names (`message_start`, `content_block_delta`, …) on the Messages side |
| Errors | OpenAI envelope `{"error":{"message","type","param","code"}}` |
| Rate limits | `x-ratelimit-*` headers; `Retry-After` on `429` and `503` |

The router forwards both API styles unchanged apart from redaction, so
an OpenAI SDK and an Anthropic SDK can both use the same key. That is
what lets Claude Code (Anthropic Messages, base `/llm/meta`) and Codex
(Responses, base `/llm/meta/v1`) run on a Meta key with a Meta model
picked from `GET /v1/models` (`docs/agents.md`, Choosing a provider).

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

## Muse Code

Checked 2026-10-29 at <https://dev.meta.ai/docs/muse-code> against
`muse` 1.4.14, with a local request logger standing in for the API.
Used by `plugins/agents/musecode` (`docs/agents.md`).

| Fact | Value |
|---|---|
| Install | `curl -fsSL https://dev.meta.ai/install.sh \| sh`; the binary is `muse` |
| Version | `muse --version` prints `Muse Code 1.4.14 (1.4.14-R7309.1)`; `MUSE_NO_AUTO_UPDATE=1` stops the launcher updating itself |
| Settings | `$XDG_CONFIG_HOME/muse/settings.json` (auth and trust files move with it); needs `schema_version: 1`; `provider` is a plain string |
| Endpoint | `endpoint_transport: {base_url, auth: "bearer"}`; the key comes from `META_API_KEY` |
| Calls | `POST <base_url>/responses` (OpenAI Responses API, streamed): Muse appends only `/responses`, so the base must end in `/v1`. Also `GET <origin>/muse-code/models` at the host root, whose failure Muse tolerates |
| MCP | `mcpServers.<name>: {type: "streamable-http", url, headers, required}`; header values are literal |
| Hooks | `hooks.PreToolUse: [{matcher, hooks: [{type: "command", command, timeout, statusMessage}]}]`; stdin carries `hook_event_name`, `tool_name`, `tool_input` and more; the env is cleared to an allowlist |
| Hook answer | exit `0` allows (stdout starting `{` is read as JSON); exit `2` blocks with stderr as the reason; any other exit is a failed hook and the call goes on |
| Flags | `--model`, `--approval-judge off`, `--disable-approval` (keeps the sandbox), `--trust-workspace`, `-c KEY=VALUE` |

## Claude Code

Checked 2026-10-29 at <https://code.claude.com/docs/en/hooks>,
<https://code.claude.com/docs/en/settings>,
<https://code.claude.com/docs/en/cli-reference>,
<https://code.claude.com/docs/en/mcp>,
<https://code.claude.com/docs/en/env-vars>,
<https://code.claude.com/docs/en/llm-gateway-connect>,
<https://code.claude.com/docs/en/llm-gateway-protocol> and
<https://code.claude.com/docs/en/setup>. Used by
`plugins/agents/claudecode` (`docs/agents.md`).

| Fact | Value |
|---|---|
| Install | `curl -fsSL https://claude.ai/install.sh \| bash` (also Homebrew, WinGet, npm); the binary is `claude` |
| Version | `claude --version` prints `2.1.211 (Claude Code)` |
| Config dir | `CLAUDE_CONFIG_DIR` moves settings, history and plugins out of `~/.claude` |
| Settings | precedence: managed, then `--settings <file or JSON>`, then project local, project, user; strict JSON; lists merge |
| Gateway | `ANTHROPIC_BASE_URL`; `ANTHROPIC_AUTH_TOKEN` goes in `Authorization: Bearer`, `ANTHROPIC_API_KEY` in `x-api-key` (and asks once in interactive mode) |
| Models | `ANTHROPIC_MODEL`; `ANTHROPIC_DEFAULT_HAIKU_MODEL` (also background work), `_SONNET_`, `_OPUS_` set the aliases |
| Calls | `POST /v1/messages?beta=true`, optional `POST /v1/messages/count_tokens`, `HEAD /api/hello` at startup, `GET /v1/models` only with model discovery on |
| Traffic | `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` turns off updates, telemetry and error reports |
| MCP | `--mcp-config <file>` with `{"mcpServers": {"<name>": {"type": "http", "url", "headers"}}}`; credential variables read as empty in remote `url` and `headers` |
| Hooks | `hooks.PreToolUse: [{matcher: "*", hooks: [{type: "command", command, timeout}]}]`; timeout in seconds, default 600; hooks inherit Claude Code's environment |
| Hook stdin | `session_id`, `transcript_path`, `cwd`, `permission_mode`, `hook_event_name`, `tool_name`, `tool_input`, `tool_use_id` |
| Hook answer | exit `0` applies JSON output (`hookSpecificOutput.permissionDecision`: `allow`, `deny`, `ask`, `defer`); silence falls through to the normal permission flow; exit `2` blocks with stderr as the reason; any other exit, or a timeout, is a non-blocking error and the call goes on |

## Codex

Checked 2026-10-29 at the Codex docs (<https://developers.openai.com/codex>,
which now redirects to <https://learn.chatgpt.com/docs/config-file/config-reference>,
<https://learn.chatgpt.com/docs/hooks>,
<https://learn.chatgpt.com/docs/extend/mcp?surface=cli> and
<https://learn.chatgpt.com/codex/cli>) and
<https://github.com/openai/codex/blob/main/docs/config.md>. Used by
`plugins/agents/codex` (`docs/agents.md`).

| Fact | Value |
|---|---|
| Install | `curl -fsSL https://chatgpt.com/codex/install.sh \| sh` (also `npm i -g @openai/codex`); the binary is `codex`; `codex --version` prints `codex-cli 0.165.0` |
| Home | `CODEX_HOME` (default `~/.codex`) holds `config.toml`, `hooks.json`, `auth.json` and logs |
| Provider | `model_provider = "<id>"` selects `[model_providers.<id>]`: `name`, `base_url`, `env_key`, `wire_api` (only `"responses"`), `http_headers`, `env_http_headers`, `query_params`; ids `openai`, `ollama`, `lmstudio` are reserved |
| MCP | `[mcp_servers.<name>]`: `url`, `bearer_token_env_var`, `http_headers`, `env_http_headers`, `enabled`, `required`, `startup_timeout_sec` (10), `tool_timeout_sec` (60) |
| Hooks | `hooks.json` or `[hooks]`; on by default, `[features] hooks` (alias `codex_hooks`) turns them off; events include `PreToolUse` and `PermissionRequest`; timeout in seconds, default 600. A user hook runs only once trusted: `[hooks.state."<file>:pre_tool_use:<group>:<handler>"] trusted_hash = "sha256:…"`, a hash of the normalized hook (checked against 0.165.0) |
| Hook stdin | `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`, `turn_id`, `tool_name`, `tool_use_id`, `tool_input` |
| Hook answer | exit `0` with no output goes on; exit `2` with stderr, or `permissionDecision: "deny"`, blocks; an unsupported output field marks the hook failed and the call goes on |
| Flags | `--model`, `-c key=value`, `--ask-for-approval` (`on-request`, `never`), `--sandbox` (`read-only`, `workspace-write`, `danger-full-access`) |

## Web Push

Checked 2026-10-29 at <https://www.rfc-editor.org/rfc/rfc8030>,
<https://www.rfc-editor.org/rfc/rfc8291> and
<https://www.rfc-editor.org/rfc/rfc8292>. Used by `internal/push`
(`docs/approvals.md`). Tested against the RFC 8291 Appendix A vector
byte for byte, and against a test subscriber that decrypts the notice
and checks the VAPID signature.

| Fact | Value |
|---|---|
| Send (RFC 8030) | `POST` the encrypted body to the subscription endpoint with a `TTL` header (seconds) and, for approvals, `Urgency: high`. `201 Created` is success |
| Gone (RFC 8030) | `404` or `410` from the push service: the subscription has ended; forget it |
| Encryption (RFC 8291) | `Content-Encoding: aes128gcm`: ECDH on P-256 between a fresh key and the browser's `p256dh`, HKDF-SHA-256 with the 16-byte `auth` secret (`WebPush: info\0` + both public keys), then the CEK (`Content-Encoding: aes128gcm\0`, 16 bytes) and nonce (`Content-Encoding: nonce\0`, 12 bytes). One record; the header holds the salt, `rs` = 4096 and the sender's public key; the plaintext ends with the `0x02` padding delimiter |
| VAPID (RFC 8292) | `Authorization: vapid t=<JWT>, k=<base64url public key>`. The JWT is ES256 (raw `r\|\|s` signature) with `aud` = the endpoint's origin, `exp` at most 24 hours ahead (Devcules uses 12), and `sub` a `mailto:` or `https:` contact |
| Keys | The VAPID signing key is never the ECDH key: each message gets a fresh ECDH key |
| Browser side | `PushManager.subscribe({userVisibleOnly: true, applicationServerKey})` with the server's uncompressed 65-byte public key; iPhone offers it only to a web app added to the Home Screen |
