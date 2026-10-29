# Audit log and traces

M2.4 adds two records. The **audit log** says who did what, when and
from where. **Traces** say what each agent launch asked its model and
what came back. Both live in serve's store and show in the Activity
tab.

## Audit log

`internal/audit` appends one entry per action:

| Action | Recorded when | Target, detail |
|---|---|---|
| `auth.sign_in` | A password or passkey sign-in succeeds | `method` |
| `auth.sign_in_failed` | A sign-in is refused (actor `anonymous`: the typed username is never kept, since people paste passwords into it) | `method`, `reason` (`invalid` or `throttled`) |
| `auth.sign_out` | A session signs out | |
| `passkey.add`, `passkey.rename`, `passkey.remove` | Passkey changes | credential id, new `name` |
| `terminal.open`, `terminal.close` | Terminals from the browser | terminal id |
| `redact.policy` | The redaction policy changes | each changed type, `old → new` |
| `launch.mint`, `launch.revoke` | Launch tokens | launch id, `label`, `provider`, `expires` |
| `agent.launch` | Starting an agent | terminal id, `agent`, `runner`, `launch`, `model` |
| `trace.read` | Someone opens a trace | launch id |
| `audit.export` | The log is exported (API or CLI) | |
| `vault.rotate` | `devcules vault rotate` (actor `cli`) | `from`, `to` key ids |
| `approval.ask` | An agent's call waits for the owner (actor `launch:<id>`) | approval id, `runner`, `agent`, `tool`, `rule`. Never the command (`docs/approvals.md`) |
| `approval.allow`, `approval.deny` | The owner answers | approval id, `runner`, `agent`, `tool`, `rule` |
| `approval.expire` | Nobody answered in time (actor `system`) | approval id |
| `approval.blocked` | The access mode denied a call outright (actor `launch:<id>`) | tool name, `runner`, `mode`, `rule` |
| `approval.mode` | An access mode changes | target, `from`, `to` |
| `approval.rules` | The critical rules change | each changed rule id: `added`, `removed`, `changed`, `on` or `off` |
| `push.subscribe`, `push.remove` | A device turns notifications on, or is removed | device id, `label` |
| `mcp.call` | An agent calls a tool on the MCP server (actor `launch:<id>`) | tool name, `label`, `error` when it failed (`docs/mcp.md`). Never the arguments |

Every entry has a sequence number, a UTC time, the actor, the action,
the target, the client IP (the connection's, no proxy headers) and a
small detail map. Values are clipped to 200 bytes and 16 details.
Nothing secret goes in: no password, token, key or redacted value.

### Append-only, three ways

1. The store's audit repo has `Append` and reads only. `Append` takes
   exactly the next sequence number or fails with `ErrConflict`, so
   two writers (serve and a CLI command) can never fork the log; the
   loser retries.
2. SQLite triggers refuse every `UPDATE` and `DELETE` on `audit_log`,
   even from a SQL shell on the database.
3. Each entry stores `hash = SHA-256("devcules-audit-v1\n" ‖
   prevHash ‖ JSON[seq, at, actor, action, target, ip, detail])`, with
   `prevHash` the hash of the entry before (32 zero bytes for entry 1).
   An edit made around the database (a hand-edited file with the
   triggers dropped, a backup restored with rows removed) breaks the
   chain at that entry.

Serve walks the chain at start and logs `audit chain verified` with the
count, or `audit chain broken` with the entry and the reason. It still
starts: the owner needs the UI to see what happened.

### Reading and exporting

| Route | Does |
|---|---|
| `GET /api/audit?before=&limit=` | Entries newest first, up to `limit` (default 100, max 500) below `before` |
| `GET /api/audit/export` | The whole log as JSON lines (`application/x-ndjson`, a download), oldest first, hashes included |
| `GET /api/audit/export?verify=1` | `{ok, checked, brokenAt?, reason?}` from a full chain walk |

All need a session. From the shell, beside serve:

```sh
docker compose -f deploy/compose.yaml exec serve devcules audit export > audit.jsonl
devcules audit verify audit.jsonl   # checks a file with no database
docker compose -f deploy/compose.yaml exec serve devcules audit verify
```

The log is never pruned.

## Traces

The router hands every call that passed the token check to
`internal/trace` (see `docs/router.md`). One trace row holds:

- the launch id, time, provider, path, model, status, duration and
  whether it streamed;
- input and output tokens, from the provider's `usage`;
- redaction counts per type;
- the question (the last user message with text), the reply text, and
  each tool call's name and arguments.

Text is what the provider saw: masked values are placeholders. A type
in `log` mode is sent, and so stored, as written. A blocked request
keeps its counts and no text. Text is capped: 32 KiB for the question
and the reply, 8 KiB per tool call's arguments, 64 tool calls.

| Route | Does |
|---|---|
| `GET /api/traces` | One summary per launch with calls, latest first: calls, tokens, redactions, first and last call, and the launch's label |
| `GET /api/traces/{launchID}` | That launch's calls, oldest first. Audited as `trace.read`; `404` when there is no such launch and no calls |

Traces older than `DEVCULES_TRACE_DAYS` (default 30, `0` keeps them)
are pruned at start and once a day.
