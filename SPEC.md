# SPEC.md: Devcules — The Sovereign Agent Control Room & Collaboration Engine

**License:** Apache-2.0  
**Repository:** `github.com/devcules/devcules`  
**Go Module:** `github.com/devcules/devcules`  
**Target Platform:** Linux / macOS / Docker / Tailscale / Mobile PWA  

---

## 1. Executive Summary & Value Proposition

> **"Agents lift heavy. Teams make them better. You stay in control."**

**Devcules** is an open-source, self-hosted control plane for autonomous AI coding agents. It provides a hardened security boundary, real-time secret redaction, phone-first Face ID execution approvals, visual computer grounding via Meta’s Segment Anything Model (SAM), and a human-in-the-loop collaboration loop powered by ScrumDo (governance) and Spryng Instrument (stakeholder feedback).

### Why It Wins Meta’s Hackathon / Open Source Competition
1. **Deep Native Meta Ecosystem Integration:**
   - **Llama Stack & Meta Model API:** Built as the default runtime provider for LLM completions, structured agent tool calls, and local edge fallback (via Ollama / vLLM).
   - **Llama Guard 3 & Purple Llama:** Real-time semantic safety classifier running alongside deterministic regex/Gitleaks redaction on all agent prompts and tool invocations.
   - **SAM (Segment Anything Model) Visual Grounding:** Zero-shot coordinate resolution for canvas-rendered, non-DOM, or remote desktop UI elements during agent computer-use testing.
2. **Sovereign, Self-Hosted Architecture:** No middleman SaaS. Developers maintain total custody of model weights, API tokens, terminal streams, and proprietary codebases.
3. **Solves the #1 Agent Bottleneck:** Moves beyond single-user toy terminal wrappers into verifiable enterprise team governance: specifications are ingested from work trackers, risky steps require one-touch mobile approval, stakeholder reviews are collected at key milestones, and learned lessons are shared across multiple agent runtimes (Llama Stack Agent, Claude Code, Codex).

---

## 2. Core Operating Concepts & Trust Triad

To prevent conflating administrative rights with code reviews, Devcules enforces three strict, independent concepts:

| Concept | Question Answered | Authority / Mechanism | Isolation Guarantee |
| :--- | :--- | :--- | :--- |
| **Execution Approval** | *"May this exact destructive shell command or migration run right now?"* | Devcules Operator via Phone PWA (WebAuthn / Face ID) | Bound to a single-use action cryptographic hash. |
| **Stakeholder Feedback** | *"Does this feature or UI match user and design expectations?"* | Teammates via Spryng Instrument (no server/terminal access needed) | Stored as structured dispositions (`accepted`, `rejected`, `deferred`). |
| **Work Acceptance** | *"Does the completed changeset satisfy ticket acceptance criteria?"* | ScrumDo / Git PR status checks | External governance boundary; cannot be triggered by terminal script. |

---

## 3. System Architecture & Topology

Devcules is structured as a **modular monolith** with a single trust split. Untrusted agent processes never share memory or filesystems with real API keys or vault storage.

```
                           +---------------------------------------+
                           |  Phone PWA / Desktop Operator Console |
                           |  (Tailscale Encrypted HTTPS)          |
                           +-------------------+-------------------+
                                               |
                                               v
+----------------------------------------------------------------------------------------------------+
| devcules serve (Control Plane - Trusted Zone)                                                      |
|                                                                                                    |
|  [External Adapters]                  [Governance & Collaboration]                                 |
|   * ScrumDo (Work ingestion)           * internal/work       (Spec & Git ref normalization)        |
|   * Spryng (Feedback collection)       * internal/milestones (Advisory vs Blocking checkpoints)    |
|                                        * internal/feedback   (Disposition state machine)           |
|                                        * internal/lessons    (Cross-agent memory ledger)           |
|                                                                                                    |
|  [Security & Redaction]               [Control Services]                                           |
|   * internal/vault (AES-256-GCM)       * internal/approvals (Single-use hash policy engine)        |
|   * internal/redact (Gitleaks+Llama)   * internal/push      (Web Push / VAPID)                     |
|   * internal/router (/llm/{provider})  * internal/audit     (Append-only signed JSONL log)         |
|   * internal/mcp (Devcules MCP Server) * internal/store     (Pure-Go SQLite engine via modernc)    |
+--------------------------------------------------+-------------------------------------------------+
                                                   |
                                                   | Bidirectional WebSocket ("link" protocol)
                                                   | Authenticated via ephemeral runner token
                                                   v
+----------------------------------------------------------------------------------------------------+
| devcules runner (Untrusted Execution Zone)                                                         |
|                                                                                                    |
|  * tmux Terminal Session Manager & xterm.js PTY bridge                                             |
|  * Agent Adapters: Llama Stack Code Agent (Default), Claude Code, OpenAI Codex                     |
|  * Sandboxed Workspace: Mounted customer code volume (/workspace)                                  |
|  * Ephemeral Launch Token: Only valid against local devcules serve router                          |
+--------------------------------------------------+-------------------------------------------------+
                                                   |
                                                   | WebSocket ("link" protocol)
                                                   v
+----------------------------------------------------------------------------------------------------+
| devcules machine (Optional Remote/Local Computer Helper)                                           |
|                                                                                                    |
|  * OS Accessibility Trees (macOS AX, Windows UIA, Linux AT-SPI)                                    |
|  * Input & Screenshot Driver (X11 / Wayland / Quartz / Win32)                                       |
|  * SAM (Segment Anything) Zero-Shot Visual Grounding Engine                                        |
+----------------------------------------------------------------------------------------------------+
```

---

## 4. Package Structure & Module Boundary Rules

```
cmd/devcules/                  # CLI binary entrypoint (serve, runner, machine, hook, health)
internal/
  config/                      # Strict env & file configuration parsing
  store/                       # Store interface + SQLite (modernc, zero cgo) implementation
  bus/                         # Bus interface + in-memory pub/sub
  auth/                        # WebAuthn (Passkeys), session cookies, CSRF tokens
  vault/                       # AES-256-GCM secret cipher; never dumps plaintext to logs
  redact/                      # Gitleaks regex scanner + PII HMAC masks + Llama Guard 3 hook
  router/                      # /llm/{provider} proxy, ephemeral launch token verification
  audit/                       # Immutable event log; JSON-lines export
  approvals/                   # Policy evaluation, single-use action hash computation, push fan-out
  push/                        # Web Push (VAPID) engine for mobile notifications
  work/                        # Domain models for external task specs & git anchors
  context/                     # Context packager: spec + git tree diffs + applicable lessons
  milestones/                  # Execution checkpoints: lifecycle of advisory & blocking milestones
  feedback/                    # Feedback ingestion, normalization, and disposition records
  lessons/                     # Approved organizational lessons & semantic tag matcher
  mcp/                         # Devcules native Model Context Protocol server
  link/                        # Multiplexed WebSocket protocol (both serve and runner ends)
  terminals/                   # tmux process wrapper & PTY stream multiplexer
  api/                         # HTTP REST & WebSocket API handlers
  web/                         # Embedded React 18 / Vite PWA (go:embed)
plugins/
  providers/                   # Model adapters: meta/ (Meta Model API / Llama Stack), ollama/
  agents/                      # Agent CLI wrappers: llamastack/, claudecode/, codex/
  work/                        # Work sources: scrumdo/, file/
  feedback/                    # Feedback sources: spryng/, file/
  machines/                    # Screen/input drivers + sam/ (Segment Anything grounding)
web/                           # React 18, TypeScript, Tailwind CSS, Vite PWA source
```

### Architectural Invariants
1. **Dependencies Point Inward:** `plugins/` can import `internal/`, but `internal/` must **never** import `plugins/`. Adapters register themselves via interfaces at initialization.
2. **Untrusted Runners Hold No Long-Lived Secrets:** Real API keys reside exclusively in `internal/vault` inside the `serve` process. The runner only ever receives an ephemeral, revocable, single-use launch token scoped strictly to `devcules serve /llm/`.
3. **No Unauthenticated Listening Sockets:** `runner` and `machine` daemons **never** bind public ports; they dial out to `serve` over WebSocket.

---

## 5. Security & Safety Model

### 5.1 Dual-Engine Redaction: Deterministic Regex + Llama Guard 3
Every outbound prompt to an LLM provider and inbound model response flows through `internal/redact`:
1. **Deterministic Redaction:** Scans payload using Gitleaks detection rules + PII regex. Secrets are replaced with deterministic placeholders: `{{DEVCULES_REDACT:<HMAC-SHA256(Secret, VaultKey)>}}`.
2. **Semantic Safety with Llama Guard 3:** When operating in high-security mode, prompts and tool-call parameters are evaluated against Meta's Llama Guard 3 safety taxonomies (detecting prompt injections, unauthorized privilege escalation, and malicious code payloads).
3. **Restoration Rules:** Masked placeholders are only restored in the direct HTTP response stream returned to the active agent process. **Secrets are never restored into persistent logs, traces, Spryng feedback cards, or Web Push notifications.**

### 5.2 Cryptographically Bound Execution Approvals
When an agent attempts a high-risk operation (`rm -rf`, `sudo`, `DROP TABLE`, `git push --force`, `ALTER TABLE`, or disk modifications):
1. The execution is halted by the runner hook.
2. An `ActionRequest` is created with a single-use action hash:
   $$\text{ActionHash} = \text{HMAC-SHA256}(\text{SessionID} \parallel \text{ExactCommand} \parallel \text{WorkingDir} \parallel \text{Nonce}, \text{VaultKey})$$
3. A Web Push notification alerts the operator's phone.
4. When the operator taps **Allow Once** via WebAuthn/Face ID, the signed approval is sent to `serve`.
5. The runner executes the command **only if** the command to be executed matches the verified `ActionHash`. If the agent alters a single character, execution is aborted.

---

## 6. Machine Automation & Meta SAM Grounding

When an agent needs to verify UI changes on a live screen:
1. `devcules machine` first queries the operating system accessibility tree (macOS AX, Windows UI Automation, or Linux AT-SPI).
2. If the UI element is missing from the accessibility tree (e.g., custom HTML5 `<canvas>`, WebGL, Flutter/Unity desktop UI, or legacy remote desktops), the screenshot is routed to the **SAM (Segment Anything)** module.
3. The agent passes a natural language or box prompt (e.g., `"Click the Pay Now button"`).
4. SAM generates an instance segmentation mask over the button.
5. `devcules machine` calculates the geometric centroid $(X_c, Y_c)$ of the mask:
   $$X_c = \frac{1}{N}\sum_{i=1}^N x_i, \quad Y_c = \frac{1}{N}\sum_{i=1}^N y_i$$
6. The driver issues hardware-level click events to $(X_c, Y_c)$.
7. An **Action Budget** (default: 40 actions) and a **Done-Verifier** (requiring post-click visual confirmation) prevent infinite runaway loops.

---

## 7. Data Contracts & Domain Models

```go
package domain

import "time"

// WorkReference represents an ingested external specification
type WorkReference struct {
    ID          string    `json:"id"`
    Provider    string    `json:"provider"`    // "scrumdo", "github", "file"
    ExternalID  string    `json:"external_id"`
    Title       string    `json:"title"`
    SpecBody    string    `json:"spec_body"`
    GitRef      string    `json:"git_ref"`
    Version     int       `json:"version"`
    CreatedAt   time.Time `json:"created_at"`
}

// ContextPackage is the complete, versioned prompt package provided to an agent
type ContextPackage struct {
    PackageVersion   int           `json:"package_version"`
    WorkRef          WorkReference `json:"work_ref"`
    ApplicableRules  []Lesson      `json:"applicable_rules"`
    UnresolvedIssues []string      `json:"unresolved_issues"`
    GeneratedAt      time.Time     `json:"generated_at"`
}

// Milestone checkpoint classification
type MilestoneType string
const (
    MilestoneAdvisory MilestoneType = "advisory" // Agent continues working
    MilestoneBlocking MilestoneType = "blocking" // Agent waits for human disposition
)

type Milestone struct {
    ID           string        `json:"id"`
    SessionID    string        `json:"session_id"`
    Name         string        `json:"name"`
    Type         MilestoneType `json:"type"`
    GitCommitSHA string        `json:"git_commit_sha"`
    DiffSummary  string        `json:"diff_summary"`
    CollectorURL string        `json:"collector_url"` // Spryng survey / review link
    Status       string        `json:"status"`        // "pending", "resolved"
}

// FeedbackItem captures teammate responses from Spryng Instrument
type DispositionState string
const (
    DispositionAccepted  DispositionState = "accepted"
    DispositionRejected  DispositionState = "rejected"
    DispositionDeferred  DispositionState = "deferred"
    DispositionNeedsClar DispositionState = "needs_clarification"
)

type FeedbackItem struct {
    ID              string           `json:"id"`
    MilestoneID     string           `json:"milestone_id"`
    Author          string           `json:"author"`
    Comment         string           `json:"comment"`
    TargetFile      string           `json:"target_file,omitempty"`
    SuggestedFix    string           `json:"suggested_fix,omitempty"`
    Disposition     DispositionState `json:"disposition"`
    ReconciledInVer int              `json:"reconciled_in_version"`
}

// Lesson encapsulates organizational memory shared across disparate agents
type Lesson struct {
    ID           string    `json:"id"`
    Title        string    `json:"title"`
    Rule         string    `json:"rule"`
    Tags         []string  `json:"tags"`          // e.g. ["postgres", "react", "tailwindcss"]
    OriginTicket string    `json:"origin_ticket"`
    ApprovedBy   string    `json:"approved_by"`
    Active       bool      `json:"active"`
    CreatedAt    time.Time `json:"created_at"`
}
```

---

## 8. API Specification

### 8.1 REST Endpoints

| Method | Endpoint | Description | Auth |
| :--- | :--- | :--- | :--- |
| `GET` | `/healthz` | Process liveness check | None |
| `GET` | `/readyz` | Readiness check (DB migrations applied, runner linked) | None |
| `POST` | `/api/v1/auth/passkey/login` | WebAuthn passkey assertion | WebAuthn Challenge |
| `GET` | `/api/v1/work/items` | List ingested ScrumDo / local work specs | Session Cookie |
| `POST` | `/api/v1/sessions/launch` | Launch agent session with specified work spec | Session Cookie |
| `POST` | `/api/v1/approvals/:id/decide` | Approve ("allow_once") or Deny an action request | Session Cookie (Passkey Re-auth) |
| `POST` | `/api/v1/feedback/webhook` | Ingest teammate review responses from Spryng | Bearer Webhook Secret |
| `POST` | `/api/v1/lessons` | Propose or approve a reusable engineering lesson | Session Cookie |
| `POST` | `/llm/v1/chat/completions` | Redacting model router (OpenAI/Meta compatible) | Ephemeral Runner Token |

### 8.2 WebSocket "Link" Protocol (`/link/v1`)
Runners and Machine helpers dial `wss://serve:7659/link/v1`. Frame structure:
```json
{
  "v": 1,
  "type": "terminal:data" | "approval:request" | "approval:resolve" | "machine:ground",
  "id": "frame-uuid-v4",
  "data": { }
}
```

---

## 9. Phased Implementation Plan & Test Gates

Each module corresponds to a single, atomic pull request with strict unit, contract, and integration coverage.

```
Stage A: Foundation & Link ───────► Stage B: Redacted Execution ──────► Stage C: Context & Governance
 (Config, SQLite, Passkeys,          (Vault, Gitleaks+Llama Guard,       (ScrumDo Ingestion,
  WebSocket Link Protocol)            Approvals, Agent Runner)            Context Bundles, Milestones)
                                                                                  │
                                                                                  ▼
Stage F: Meta Showcase & Polish ◄─── Stage E: Cross-Agent Memory ◄─── Stage D: Feedback Loop
 (Compose, Tailscale Profile,         (Lesson Ledger, Tag Matcher,        (Spryng Webhooks,
  SAM Grounding, 90s Demo)             Multi-Agent Portability)            Feedback Dispositions)
```

### Stage A: Core Foundation & Communication Link
- **PR A.1:** Repo skeleton, CI/CD pipeline, Apache-2.0 license headers, import architecture validator.
- **PR A.2:** `internal/config`, `cmd/devcules` CLI structure, `/healthz` and `/readyz` probes.
- **PR A.3:** `internal/store` pure-Go SQLite persistence layer (`modernc.org/sqlite`) with schema migrations.
- **PR A.4:** `internal/auth` admin setup, session cookies, CSRF protection, WebAuthn Passkeys.
- **PR A.5:** `internal/link` bidirectional multiplexed WebSocket protocol with fuzzing tests.
*Gate A:* Docker Compose boots clean; runner dials into serve; WebAuthn login succeeds in headless Playwright test.

### Stage B: Redacted Execution & Approval Engine
- **PR B.1:** `internal/vault` AES-256-GCM authenticated encryption for provider keys.
- **PR B.2:** `internal/redact` Gitleaks regex engine + deterministic HMAC substitution + Llama Guard 3 hook.
- **PR B.3:** `internal/router` OpenAI/Meta Model API compatible proxy with streaming redaction.
- **PR B.4:** `internal/approvals` policy evaluator and single-use `ActionHash` computation.
- **PR B.5:** `internal/terminals` tmux session management and streaming PTY bridge.
- **PR B.6:** `plugins/agents/llamastack` agent wrapper with auto-injected approval hooks.
*Gate B:* Launching an agent attempting `rm -rf /` triggers an approval prompt; unapproved command halts; masked fake AWS keys return intact over the HTTP stream.

### Stage C: Work Item Context & Milestones
- **PR C.1:** `internal/work` domain models + `plugins/work/scrumdo` & `plugins/work/file` adapters.
- **PR C.2:** `internal/context` deterministic context packaging (ticket spec + git diff + active rules).
- **PR C.3:** `internal/milestones` engine supporting both advisory and blocking checkpoints.
- **PR C.4:** PWA UI (React 18 + Tailwind) at 390px mobile view for inspecting specs and active diffs.
*Gate C:* Linking a ScrumDo ticket correctly builds Context Package v1 and mounts it into the agent workspace.

### Stage D: Collaborative Feedback Loop (Spryng)
- **PR D.1:** `internal/feedback` schema, disposition state machine (`accepted`, `rejected`, `deferred`).
- **PR D.2:** `plugins/feedback/spryng` webhook receiver and test fixture harness.
- **PR D.3:** PWA Feedback Review component: operator triages incoming feedback cards on mobile.
- **PR D.4:** Context package updater: accepted feedback triggers generation of Context Package v2.
*Gate D:* Teammate feedback submitted through mock Spryng endpoint appears in PWA; accepting it generates an updated context package consumed by the agent.

### Stage E: Cross-Agent Memory & Portability
- **PR E.1:** `internal/lessons` repository, semantic tag indexing, and query matcher.
- **PR E.2:** Post-run lesson extraction using Llama 3.3 summarization prompt.
- **PR E.3:** `plugins/agents/claudecode` and `plugins/agents/codex` adapter implementations.
- **PR E.4:** Cross-agent retrieval test: Lesson generated by Agent A is automatically injected into Agent B’s session on a related task.
*Gate E:* Verify that an approved lesson on Postgres schema migrations automatically injects into a new Claude Code or Codex session.

### Stage F: Meta Showcase & Polish
- **PR F.1:** `plugins/machines/sam` SAM visual grounding driver (screenshot -> mask -> centroid click).
- **PR F.2:** Web Push (VAPID) phone notifications + Tailscale automated setup profile.
- **PR F.3:** Production `docker-compose.yml` (zero-setup run in under 3 minutes).
- **PR F.4:** 90-second benchmark demo recording and README documentation assets.
*Gate F:* Fresh clone on a clean machine executes the full 90-second automated demo script without error.

---

## 10. Quickstart Deployment Guide

### Running via Docker Compose
```bash
# Clone the repository
git clone https://github.com/devcules/devcules.git
cd devcules

# Generate initial environment secrets
cat <<EOF > .env
DEVCULES_LISTEN=0.0.0.0:7659
DEVCULES_PUBLIC_URL=https://my-node.tailnet.ts.net
META_API_KEY=your_meta_model_api_or_llama_stack_key
EOF

# Launch the Control Plane and untrusted Runner
docker compose up -d

# Check startup health
docker compose exec serve devcules health
```

### Accessing the Mobile Control Room
1. Connect your phone to your Tailscale network.
2. Open `https://my-node.tailnet.ts.net` in your mobile browser.
3. Tap **Share -> Add to Home Screen** to install the PWA.
4. Scan the one-time admin setup QR code printed in the server logs to register your Face ID / Touch ID passkey.

