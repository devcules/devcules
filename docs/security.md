# Security

The threat model, the hard rules, and what to actually run for a
review. Reporting stays in `SECURITY.md`.

## Threat model

**Assets.** Provider API keys (Meta, Anthropic, OpenAI, custom), the
vault's master key, launch tokens, the runner token, enrolled machine
public keys, push subscriptions, and the setup itself (the provider
profiles, the access modes, the audit).

**Attackers.** In increasing trust:

1. **A model provider** sees exactly what the router sent it (masked)
   and answers; it can be curious, log, and try to steer the agent
   with its reply.
2. **An agent CLI on a runner** runs in tmux where the user puts it
   and has its own launch token; it is untrusted code the model is
   steering, and it can try to read its environment, exfiltrate what
   it can reach, or ask for tools it should not get.
3. **A conversational assistant** is the same shape as an agent but
   speaks to one more surface: the person. Its model can be steered
   by a prompt injection in a tool result, and it can try to invent
   ids, skip reads, retry a denied call under new wording, or quote a
   masked value back as an argument. The `assistanttest` suite seeds
   those behaviours and asserts the loop holds.
4. **The operator's browser** has the session cookie and the WebAuthn
   passkey; XSS, a stolen cookie, or a forged fetch from elsewhere on
   the desk are the risks here, mitigated by HttpOnly cookies, the
   CSRF origin check on writes, and no secrets in client storage.
5. **A person with the db file** owns the vault's ciphertexts but not
   the vault key; the key file is separate, perms 0600, and rotation
   (`vault rotate`) is the path when either leaks.

Out of scope: a compromised serve host's kernel, a stolen unlocked
vault key plus database together (one without the other is
ciphertext), and a supply-chain attack on a permissive dependency —
CI's govulncheck and gitleaks cover the dependency and repo shapes.

## The hard rules

1. **No key, no token, no vault byte ever reaches the runner or the
   browser.** The halves only ever speak the same protocols
   (model-over-router with a launch token, MCP under that token, the
   browser session-and-cookie). `internal/config.ForRole` makes a
   runner/hook/machine process that was launched with
   `DEVCULES_VAULT_KEY` or `META_API_KEY` exit at startup.
2. **Redaction happens before the model and stays masked until the
   wire back.** The router holds a session’s issued placeholders and
   restores only into bytes addressed to the agent that issued them.
   The assistant's user message is redacted before the loop stores it;
   a tool argument that *is* a placeholder is refused. Review holds:
   `internal/redact`, `internal/router` (the jsonwalk boundary),
   `internal/assistants` (the loop's `RedactIn`).
3. **Every tool call is an approval record.** Agents ask through
   `/hook/approvals`, assistants through `ApprovalsGate`; on the same
   service, same policy, same mode pick (`off`, `critical`, `all`,
   `full`), same critical rules, same push. Nothing runs on a deny and
   nothing retries a deny invisibly.
4. **The audit is append-only and says who did what.** API writes,
   approval asks/answers/time-outs, launches, trace access, passkey
   changes, MCP calls (by launch, no arguments) and assistant turns.
   JSONL export is the long-term store.
5. **The architecture test is a control, not a style check.**
   `arch_test.go` pins the repo owners (`store.Secrets` → `vault`,
   `store.LaunchTokens` → `launch`, …), the internal/plugins split and
   the `terminals/relay` ↔ `pty` cut, so a refactor cannot silently
   bridge the trust split.
6. **No redirects on upstream model calls.** Every adapter's client
   refuses to follow, so a provider that 302s never sees the bearer
   land elsewhere.

## Assistant-specific notes

The conversational layer adds one asset and one risk:

- The **helper model credential** is the default profile's key
  (`META_API_KEY` first, then its vault secret), held in memory beside
  serve and never logged; on a desk without either the assistant
  answers it is not configured, and the picker says so.
- **Prompt injection through a tool result** is the realistic attack
  on an assistant: the loop's defences are the prompt (never invent,
  never retry a deny), the placeholder-refusal on arguments, the
  duplicate-ID rejection, the 25-call turn cap, and the contract
  suite that drives a mock model through the malicious behaviours and
  asserts the loop holds. Reviewers run it as
  `go test ./internal/assistants/...`.

## How a security review runs today

CI's gate is `gofmt`, `go vet`, `staticcheck`, `govulncheck
./...`, `gitleaks` and the e2e matrix (`docs/ci.md`); locally:

    go test ./... -count=1
    go test -race ./internal/assistants/... ./internal/api/ -count=2
    go run honnef.co/go/tools/cmd/staticcheck@latest ./...
    go run golang.org/x/vuln/cmd/govulncheck@latest ./...

A review of a new subsystem reads its `doc.go` (what it says it does
and does not do), its contract suite, and the subsystem lines above it
touches; then runs the gate. Findings land as issues on the private
vulnerability reporting flow (SECURITY.md), not as public issues.
