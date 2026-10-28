# Feedback & Sensemaking Lab (Prototype for Kiran)

This branch (`users/ajay/inquiry-feedback-lab`) implements the collaborative feedback and sensemaking loop requested for Devcules, ready to be converted into the main skeleton.

## Quickstart

Run the lab with one command:
```bash
go run lab/feedback/cmd/main.go
```
This boots the HTTP server on `http://127.0.0.1:7890` and automatically pops open the interactive UI window in your browser.

---

## What Has Been Built

### 1. Popping Notifications (`notify.go`)
- **Native OS Desktop Alert:** Uses `osascript` on macOS and `notify-send` on Linux to pop an OS desktop notification with sound when an agent checkpoint is reached or when stakeholder feedback arrives.
- **In-Window SSE Stream:** Server-Sent Events endpoint (`GET /events`) continuously streams notification events directly to all active browser windows, triggering dynamic toast alerts with sound cues.
- **Web Push Format:** Helper function `FormatWebPushPayload()` outputs standard VAPID-compliant payloads ready for phone push notifications over Tailscale.

### 2. Question Lists & Inquiry Experience (`domain.go`, `server.go`)
- Supports structured sensemaking question types:
  - **Likert (1-5 scale):** Agreement with specification / requirements.
  - **Risk Assessment:** Multi-tier operational risk classification (`Low`, `Medium`, `High`, `Critical`).
  - **Edge Case Completeness:** Boundary verification (`Yes`, `Partially`, `No`).
  - **Constructive Inquiry:** Freeform guidance and critical technical notes.
- Includes a 1-click simulator button: **"Auto-Simulate 3 Teammate Responses"** (injects responses from Lead Dev, PM, and DevOps with conflicting viewpoints to immediately test polarity detection).

### 3. Data Storage: Memory + SQLite + PostgreSQL (`store.go`)
- **Dual Engine Architecture:**
  - **In-Memory / SQLite (Default):** Zero setup; runs out-of-the-box for instant local testing.
  - **PostgreSQL Ready:** Automatically activates if `DATABASE_URL` is set (e.g. `export DATABASE_URL="postgres://user:pass@localhost:5432/devcules"`). Runs standard SQL migrations for `milestones`, `feedback_responses`, and `milestone_analyses`.
- Easily maps into `internal/store` when converting to your main store interface.

### 4. Sensemaking Analysis & Polarity Mapping (`analytics.go`)
- **Consensus Index ($0.0 - 1.0$):** Measures variance across all responses to calculate overall team agreement percentage.
- **Role Polarity Detection:** Calculates divergence across functional groups (Engineering vs. Product vs. QA/Security). Automatically flags polarized questions where teams disagree.
- **Disposition Synthesizer:** Automatically outputs one of four states:
  - `accepted`: High consensus (>75%), low/medium risk.
  - `course_correct`: High/critical risk with low consensus.
  - `needs_clarification`: Divergence between teams requires operator intervention.
  - `rejected`: Low consensus, critical flaw.
- **Synthesized Context v2 (Prompt Delta):** Formats an actionable markdown prompt block containing consensus metrics, critical team comments, and role warnings that gets injected into the agent's next prompt iteration.

---

## File Structure

```
lab/feedback/
├── domain.go            # Models: Question, MilestoneSnapshot, Answer, FeedbackResponse, AnalysisResult
├── store.go             # Storage interface + MemoryStore + SQLStore (SQLite/Postgres)
├── notify.go            # NotificationHub, OS desktop pop alerts, SSE broadcaster, Web Push formatter
├── analytics.go         # Sensemaking engine: consensus index, polarity matrix, disposition synthesizer
├── server.go            # HTTP routes, SSE handler, and embedded modern dark UI
├── analytics_test.go    # Unit tests for consensus math and store lifecycle
├── README.md            # This documentation
└── cmd/
    └── main.go          # Runnable entrypoint with automatic browser launch
```

---

## How to Port to the Main Skeleton Later

When your skeleton milestones are ready:
1. Copy `domain.go` types into `internal/feedback/` and `internal/milestones/`.
2. Move `analytics.go` into `internal/feedback/sensemaking.go`.
3. Integrate `notify.go` into your `internal/push/` and event bus (`internal/bus`).
4. Wire the SQL queries from `store.go` into `internal/store/postgres` and `internal/store/sqlite`.
