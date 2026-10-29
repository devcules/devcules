# MCP server

Serve hosts the Devcules MCP server at `POST /mcp` (`internal/mcp`).
Agents launched by Devcules, and later the built-in assistant, use it
to see Devcules itself: the status, their own launch, their own model
calls and the terminals. It speaks the Model Context Protocol over
Streamable HTTP, in both the current stateless revision and the older
handshake revisions, so any MCP client of the last two years connects.

## Who may call

Every request needs a launch token (`docs/router.md`), the same one the
agent uses for the model router:

```
Authorization: Bearer dvl_...
```

No token, an unknown one, a revoked one or an expired one gets `401`
with `WWW-Authenticate: Bearer` and a JSON-RPC error saying which.
The session cookie is never accepted here. A request that carries an
`Origin` header other than `DEVCULES_PUBLIC_URL`'s origin gets `403`
(the spec's DNS rebinding rule); clients that are not browsers send no
`Origin` and are not affected.

## Protocol versions

| Version | How it is served |
|---|---|
| `2026-07-28` | Stateless. No `initialize`: every request carries `_meta` with `io.modelcontextprotocol/protocolVersion` and `io.modelcontextprotocol/clientCapabilities`, and the `MCP-Protocol-Version`, `Mcp-Method` and `Mcp-Name` (for `tools/call`) headers must match the body. `server/discover`, `tools/list` and `tools/call`. Every result has `resultType: "complete"` and `_meta` with the server's info. |
| `2025-11-25`, `2025-06-18`, `2025-03-26` | `initialize` negotiates the version (the client's when supported, else `2025-11-25`), then `ping`, `tools/list` and `tools/call` with the `MCP-Protocol-Version` header (`2025-03-26` when it is missing, as the spec says). |

A request is served as `2026-07-28` when its `_meta` names a protocol
version or its `MCP-Protocol-Version` header is `2026-07-28`; anything
else is a handshake-era client. Errors follow each revision:

| Case | Answer |
|---|---|
| Not `POST` (including `GET` and `DELETE`) | `405`, `Allow: POST` |
| Body over 1 MiB | `413` |
| A JSON array (batch) | `400`, `-32600`: one message per POST |
| Not JSON | `400`, `-32700` |
| Not JSON-RPC 2.0, a response, no method, a `null` id | `400`, `-32600` |
| A notification (no id) | `202`, no body |
| `2026-07-28`: `_meta` keys missing | `400`, `-32602` |
| `2026-07-28`: unsupported version | `400`, `-32022` with `data.supported` and `data.requested` |
| `2026-07-28`: a header does not match the body | `400`, `-32020` |
| `2026-07-28`: unknown method | `404`, `-32601` |
| Handshake era: unsupported `MCP-Protocol-Version` | `400`, `-32600` with the supported list |
| Handshake era: unknown method | `200`, `-32601` |
| Unknown tool, missing tool name, bad cursor | `200`, `-32602` |

The server never issues `Mcp-Session-Id`, never opens a stream and never
sends requests or notifications: every answer is `application/json`.

## Tools

`tools/list` is sorted by name, pages 50 at a time with an opaque
`nextCursor`, and (in `2026-07-28`) says `ttlMs: 300000` and
`cacheScope: "public"`: the tool set is fixed while serve runs, so
`listChanged` is `false`.

| Tool | Arguments | Answer |
|---|---|---|
| `status` | none | Version, linked runners, router providers, the redaction policy |
| `launch_info` | none | The caller's own launch: id, label, provider, created, expires, last used |
| `my_trace` | `limit` (1 to 100, default 20) | The caller's own model calls, latest last, with placeholders, and the total. Never another launch's |
| `list_terminals` | `runner` (optional) | The runner's terminals (tmux sessions) and when each was made |
| `check_redaction` | `text` (up to 64 KiB) | What a model call would carry under the policy in force: the text with placeholders, the findings, and the types that would block it. Nothing is sent anywhere and the counters do not move |

All five are read-only (`readOnlyHint: true`). Each answer has
`structuredContent` and the same JSON as text for older clients. A
failure the model can act on (bad arguments, no runner linked) is a
result with `isError: true`; an internal failure is logged and the
model sees only `internal error in <tool>`.

Later milestones register their own tools (`mcp.Registry.Register`):
names are 1 to 128 of `A-Z a-z 0-9 _ . -`, schemas are JSON objects,
and handlers decode arguments with `mcp.DecodeArgs`, which refuses
unknown fields.

## Limits, audit and logs

- Tool calls are rate-limited per launch: a burst of 30, then 5 a
  second. Over the limit, the call is an `isError` result saying so.
- Every tool call, rate-limited or not, is audited as `mcp.call` with
  actor `launch:<id>`, the tool as target, the launch's label, and
  `error=true` when it failed (`docs/audit.md`).
- One log line per request, `mcp request`, with the method, status,
  duration, tool, protocol version and launch id. Never the arguments,
  a result or the token.

## Connecting a client

The official MCP Python SDK connects in all three modes (`auto`, which
probes `server/discover`, a pinned `2026-07-28`, and `legacy`):

```python
from mcp import Client
from mcp.client.streamable_http import streamable_http_client, create_mcp_http_client

hc = create_mcp_http_client(headers={"Authorization": "Bearer " + token})
async with hc, Client(streamable_http_client("http://localhost:7659/mcp", http_client=hc)) as c:
    print(await c.call_tool("status", {}))
```

From M3.3 on, Devcules registers the server in each agent's own MCP
configuration when it launches the agent, with the launch's token.

## Testing

`internal/mcp` has the conformance tests: the transport guards, both
eras end to end, header and `_meta` checks, pagination, rate limits,
audit entries, and logs that never hold arguments or tokens.
`scripts/smoke.sh` calls `/mcp` from outside the container in both
eras, checks that `check_redaction` masks a fake key, and that the call
is audited.
