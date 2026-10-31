# Assistants

An **assistant** is a conversational helper that improves the setup
(PLAN.md §8): you chat with it, it answers from the live setup through
the same MCP tools its coding agents use, and every change the model
proposes is gated by the approvals policy exactly as a terminal agent's
call is. The built-in assistant today runs on the default provider
profile's key, against its chat-completions endpoint (`assistants/builtin`);
the style adapters (hermes, openclaw) are M5.2.

## The loop (internal/assistants)

The loop is the same shape as the coding-agents gateway: the model
proposes, the approvals service disposes.

- Your message is **redacted on the way in** (the settings' policy, the
  same sessions the router uses), so the model and the stored history
  only ever see `REDACTED_…_…` placeholders. A block-mode kind rejects
  the message (HTTP 422) rather than sending it.
- The model answers with text, tool calls, or both. Every tool call
  goes through `assistants.ApprovalsGate`, which asks the approvals
  service for the assistant's own access mode (`assistant:builtin`, in
  Access beside the terminal runners) and, on "Ask once", waits for the
  phone's answer before the tool runs.
- A reply's tool calls run **as written**: a duplicate `id` in one
  reply is refused for the second occurrence, and an argument that is a
  redaction placeholder is refused before the gate (quoting a masked
  value is not a value).
- The turn buffers events (`assistant.message`, `assistant.tool`) as
  they happen; the API serves them as one JSON answer when the model
  and the gate have fully played out. Chat does not stream.
- Conversations hold at most `maxMessages` model messages; a
  compaction pass folds the front of the history into one
  recalled-exchange summary between model calls, keeping every tool
  call's name for the trace. A turn chains at most `assistants.MaxTurns`
  (25) model calls before the loop ends it so a broken model cannot
  spin.

## The built-in assistant (internal/assistants/builtin)

`builtin.Builtin` wires the loop to one OpenAI-compatible
chat-completions endpoint: the default profile's base URL and model
(`muse-spark-1.3` unless the profile overrides it), with the key
resolve in order from `META_API_KEY`, then the profile's vault secret.
It is the PLAN's *built-in assistant on Muse Spark* by default, but the
adapter is endpoint-agnostic: any OpenAI-compatible URL and token run
the same helper.

It adds only the wire format on top of the loop: a model call timeout,
no-redirect (a 3xx from the upstream is an error, not a follow — the
credential never leaves to where a redirect points), and capped
bodies. It never holds a provider key anywhere but in memory beside
serve; the desk's `docs/redact.md` and `docs/router.md` hold the
boundary it speaks over.

### When it is not answering

The assistant always loads; the picker carries a one-line state when it
cannot answer normally:

- **no helper-model key yet**: set `META_API_KEY` or save the default
  provider's key in Settings → Providers. The chat loads and a sent
  message answers that the helper model could not be reached.
- **read only**: the desk has no approvals service, so the assistant
  answers from its own knowledge and never runs a tool.

## The web surface

`GET /api/assistants` lists the wired assistants for the picker;
`POST /api/assistants/{id}/chat` with `{"message", "session"}` starts
(`""` session) or continues a conversation and answers
`{"session", "events"}`; `GET /api/assistants/{id}/conversations/{sess}`
returns the stored messages. The Assistant tab in the web app is the
picker plus one chat; a tool row shows the gate verdict (`✓ ran`,
`✓ approved`, `✗ declined`) and who approved.

A sent message can answer HTTP 422 with `{"blocked": true, "error"}`
when redaction blocks it: the UI shows it was never sent.

## The contract suite (internal/assistants/assistanttest)

The test gate every implementation passes (`TestScriptedConversation`
in builtin runs it): a seeded mock model tries the things a
compromised or confused helper would — answer without reading,
sneak a `set_provider_key` behind a read, quote a placeholder as an
argument, retry a denied call, spin past the turn cap, forge a session
id — and the suite asserts the loop holds each. An adapter failing
one fails before the scripted E2E ever runs.

## What it does not do

- No cross-restart conversations (sessions are in-memory until a
  store-suite implementation lands).
- No streaming to the browser.
- No assistant-held provider key: the model call leaves serve with the
  resolved key, and the desk's redaction boundary is the router's.
