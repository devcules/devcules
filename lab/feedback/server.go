package feedbacklab

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// Server coordinates the web window, notifications, and analytics endpoints
type Server struct {
	store Store
	hub   *NotificationHub
	mux   *http.ServeMux
}

func NewServer(store Store, hub *NotificationHub) *Server {
	s := &Server{
		store: store,
		hub:   hub,
		mux:   http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.handleUI)
	s.mux.HandleFunc("GET /events", s.handleSSE)
	s.mux.HandleFunc("GET /api/milestones", s.handleListMilestones)
	s.mux.HandleFunc("GET /api/milestones/", s.handleGetMilestone)
	s.mux.HandleFunc("POST /api/milestones/notify", s.handleTriggerNotify)
	s.mux.HandleFunc("POST /api/responses", s.handleSubmitResponse)
	s.mux.HandleFunc("GET /api/analysis", s.handleGetAnalysis)
	s.mux.HandleFunc("POST /api/simulate", s.handleSimulateResponses)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	ch := s.hub.Subscribe()
	defer s.hub.Unsubscribe(ch)

	// Send initial ping
	fmt.Fprintf(w, "event: ping\ndata: connected\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-ch:
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "event: notification\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

func (s *Server) handleListMilestones(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListMilestones(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func (s *Server) handleGetMilestone(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/milestones/")
	m, err := s.store.GetMilestone(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(m)
}

func (s *Server) handleTriggerNotify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MilestoneID string `json:"milestone_id"`
		CustomText  string `json:"custom_text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	m, err := s.store.GetMilestone(r.Context(), req.MilestoneID)
	if err != nil {
		http.Error(w, "milestone not found", 404)
		return
	}

	msg := fmt.Sprintf("Agent %s reached checkpoint: %s. Stakeholder review requested.", m.AgentID, m.Title)
	if req.CustomText != "" {
		msg = req.CustomText
	}

	event := NotificationEvent{
		ID:        fmt.Sprintf("notif-%d", time.Now().UnixNano()),
		Type:      "milestone_ready",
		Title:     "Devcules Agent Milestone Checkpoint",
		Message:   msg,
		ActionURL: fmt.Sprintf("/?milestone_id=%s", m.ID),
		Severity:  "warning",
		Timestamp: time.Now(),
	}

	s.hub.PopNotification(event)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "popped", "event": event})
}

func (s *Server) handleSubmitResponse(w http.ResponseWriter, r *http.Request) {
	var resp FeedbackResponse
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	if resp.ID == "" {
		resp.ID = fmt.Sprintf("resp-%d", time.Now().UnixNano())
	}
	if resp.SubmittedAt.IsZero() {
		resp.SubmittedAt = time.Now()
	}

	if err := s.store.SaveResponse(r.Context(), &resp); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// Pop notification that feedback arrived
	s.hub.PopNotification(NotificationEvent{
		ID:        fmt.Sprintf("notif-feed-%d", time.Now().UnixNano()),
		Type:      "feedback_submitted",
		Title:     "New Teammate Feedback Recorded",
		Message:   fmt.Sprintf("%s (%s) submitted sensemaking feedback.", resp.AuthorName, resp.AuthorRole),
		ActionURL: fmt.Sprintf("/?milestone_id=%s", resp.MilestoneID),
		Severity:  "info",
		Timestamp: time.Now(),
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "saved", "id": resp.ID})
}

func (s *Server) handleGetAnalysis(w http.ResponseWriter, r *http.Request) {
	milestoneID := r.URL.Query().Get("milestone_id")
	if milestoneID == "" {
		http.Error(w, "milestone_id required", 400)
		return
	}

	m, err := s.store.GetMilestone(r.Context(), milestoneID)
	if err != nil {
		http.Error(w, "milestone not found", 404)
		return
	}

	responses, err := s.store.ListResponses(r.Context(), milestoneID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	result := AnalyzeResponses(m, responses)
	_ = s.store.SaveAnalysis(r.Context(), result)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) handleSimulateResponses(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MilestoneID string `json:"milestone_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	m, err := s.store.GetMilestone(r.Context(), req.MilestoneID)
	if err != nil {
		http.Error(w, "milestone not found", 404)
		return
	}

	// Inject 3 realistic personas demonstrating role polarity
	f5 := 5.0
	f4 := 4.0
	f2 := 2.0
	f1 := 1.0

	simulated := []FeedbackResponse{
		{
			ID:          fmt.Sprintf("sim-eng-%d", time.Now().UnixNano()),
			MilestoneID: m.ID,
			AuthorName:  "Sarah Lin",
			AuthorRole:  "Engineering",
			AuthorEmail: "sarah@company.internal",
			Answers: []Answer{
				{QuestionID: "q1_intent", ValueNumber: &f5},
				{QuestionID: "q2_risk", ValueString: "Low", ValueNumber: &f1},
				{QuestionID: "q3_edge", ValueString: "Yes"},
				{QuestionID: "q4_adjust", ValueString: "Code looks solid, indexing on table orders will handle high throughput."},
			},
			SubmittedAt: time.Now(),
		},
		{
			ID:          fmt.Sprintf("sim-devops-%d", time.Now().UnixNano()),
			MilestoneID: m.ID,
			AuthorName:  "Marcus Vance",
			AuthorRole:  "QA/Security",
			AuthorEmail: "marcus@company.internal",
			Answers: []Answer{
				{QuestionID: "q1_intent", ValueNumber: &f4},
				{QuestionID: "q2_risk", ValueString: "Critical", ValueNumber: &f4},
				{QuestionID: "q3_edge", ValueString: "Partially"},
				{QuestionID: "q4_adjust", ValueString: "CAUTION: Migration adds column without lock_timeout. In Postgres this will take an exclusive table lock and crash production checkout under peak traffic!"},
			},
			SubmittedAt: time.Now(),
		},
		{
			ID:          fmt.Sprintf("sim-prod-%d", time.Now().UnixNano()),
			MilestoneID: m.ID,
			AuthorName:  "Elena Rostova",
			AuthorRole:  "Product",
			AuthorEmail: "elena@company.internal",
			Answers: []Answer{
				{QuestionID: "q1_intent", ValueNumber: &f2},
				{QuestionID: "q2_risk", ValueString: "Medium", ValueNumber: &f2},
				{QuestionID: "q3_edge", ValueString: "Partially"},
				{QuestionID: "q4_adjust", ValueString: "Checkout error state is missing a fallback button to retry with PayPal when card fails."},
			},
			SubmittedAt: time.Now(),
		},
	}

	for _, sim := range simulated {
		_ = s.store.SaveResponse(r.Context(), &sim)
	}

	// Trigger calculation
	responses, _ := s.store.ListResponses(r.Context(), m.ID)
	analysis := AnalyzeResponses(m, responses)
	_ = s.store.SaveAnalysis(r.Context(), analysis)

	// Pop analysis complete notification
	s.hub.PopNotification(NotificationEvent{
		ID:        fmt.Sprintf("notif-ana-%d", time.Now().UnixNano()),
		Type:      "analysis_complete",
		Title:     "Sensemaking Analysis Ready",
		Message:   fmt.Sprintf("Consensus %.0f%%. Disposition: %s. Actionable guidance synthesized for agent.", analysis.ConsensusIndex*100, analysis.Disposition),
		ActionURL: fmt.Sprintf("/?milestone_id=%s", m.ID),
		Severity:  "warning",
		Timestamp: time.Now(),
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "simulated",
		"count":    len(simulated),
		"analysis": analysis,
	})
}

func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.New("index").Parse(uiHTML)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	_ = tmpl.Execute(w, nil)
}

const uiHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Devcules · Feedback & Sensemaking Lab</title>
<style>
  :root {
    --bg-base: #0c0e14;
    --bg-card: #151821;
    --bg-surface: #1e2230;
    --border: #2a3045;
    --text-main: #f1f5f9;
    --text-muted: #94a3b8;
    --accent: #3b82f6;
    --accent-hover: #2563eb;
    --warning: #f59e0b;
    --danger: #ef4444;
    --success: #10b981;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
  body { background: var(--bg-base); color: var(--text-main); min-height: 100vh; padding: 24px; }
  .container { max-width: 1100px; margin: 0 auto; }
  header { display: flex; justify-content: space-between; align-items: center; border-bottom: 1px solid var(--border); padding-bottom: 16px; margin-bottom: 24px; }
  .badge { background: #1e3a8a; color: #93c5fd; padding: 4px 10px; border-radius: 9999px; font-size: 12px; font-weight: 600; }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 24px; }
  @media (max-width: 860px) { .grid { grid-template-columns: 1fr; } }
  .card { background: var(--bg-card); border: 1px solid var(--border); border-radius: 12px; padding: 20px; margin-bottom: 24px; }
  h2 { font-size: 1.15rem; margin-bottom: 14px; display: flex; align-items: center; gap: 8px; color: #e2e8f0; }
  p.sub { font-size: 0.88rem; color: var(--text-muted); margin-bottom: 16px; }
  .btn { background: var(--accent); color: white; border: none; padding: 10px 16px; border-radius: 8px; font-weight: 600; cursor: pointer; transition: 0.15s; font-size: 14px; }
  .btn:hover { background: var(--accent-hover); }
  .btn-secondary { background: var(--bg-surface); border: 1px solid var(--border); color: var(--text-main); }
  .btn-secondary:hover { background: #272d3f; }
  .btn-warning { background: var(--warning); color: #000; }
  .btn-warning:hover { background: #d97706; }
  .btn-group { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 10px; }
  pre { background: #080a0f; padding: 12px; border-radius: 8px; font-size: 12px; overflow-x: auto; color: #38bdf8; border: 1px solid #1e293b; margin: 12px 0; }
  .form-group { margin-bottom: 16px; }
  label { display: block; font-size: 13px; font-weight: 600; color: #cbd5e1; margin-bottom: 6px; }
  input, select, textarea { width: 100%; background: #0a0c10; border: 1px solid var(--border); border-radius: 6px; padding: 8px 12px; color: white; font-size: 14px; }
  input:focus, select:focus, textarea:focus { border-color: var(--accent); outline: none; }
  .scale-options { display: flex; gap: 8px; }
  .scale-btn { flex: 1; padding: 8px; text-align: center; background: var(--bg-surface); border: 1px solid var(--border); border-radius: 6px; cursor: pointer; font-size: 12px; }
  .scale-btn.active { background: var(--accent); border-color: var(--accent); font-weight: bold; }
  /* Notification Toast */
  #toast-container { position: fixed; top: 20px; right: 20px; z-index: 9999; display: flex; flex-direction: column; gap: 10px; max-width: 380px; }
  .toast { background: #1e293b; border: 1px solid #3b82f6; border-left: 5px solid #3b82f6; border-radius: 8px; padding: 14px; box-shadow: 0 10px 25px rgba(0,0,0,0.5); animation: slideIn 0.25s ease-out; }
  .toast.warning { border-left-color: var(--warning); }
  .toast.critical { border-left-color: var(--danger); }
  .toast-title { font-weight: 700; font-size: 14px; color: #f8fafc; margin-bottom: 4px; display: flex; justify-content: space-between; }
  .toast-body { font-size: 13px; color: #cbd5e1; line-height: 1.4; }
  @keyframes slideIn { from { transform: translateX(100%); opacity: 0; } to { transform: translateX(0); opacity: 1; } }
  .metric-card { display: flex; gap: 16px; margin-bottom: 16px; }
  .metric-box { flex: 1; background: var(--bg-surface); border-radius: 8px; padding: 14px; border: 1px solid var(--border); text-align: center; }
  .metric-val { font-size: 24px; font-weight: 800; color: #60a5fa; margin-top: 4px; }
  .polar-tag { display: inline-block; padding: 3px 8px; border-radius: 4px; font-size: 11px; font-weight: bold; margin-left: 6px; }
  .polar-tag.alert { background: #7f1d1d; color: #fca5a5; }
  .polar-tag.ok { background: #064e3b; color: #6ee7b7; }
</style>
</head>
<body>

<div id="toast-container"></div>

<div class="container">
  <header>
    <div>
      <h1 style="font-size: 1.5rem; font-weight: 800; letter-spacing: -0.5px;">Devcules <span style="font-weight: 400; color: var(--text-muted);">| Feedback & Sensemaking Lab</span></h1>
      <p style="font-size: 0.85rem; color: var(--text-muted); margin-top: 4px;">Milestone checkpoint notification, question list collection, and consensus mapping.</p>
    </div>
    <div style="display: flex; gap: 10px; align-items: center;">
      <span class="badge" id="sse-status">● SSE Live</span>
      <button class="btn btn-warning" onclick="triggerNotify()">🔔 Pop Test Notification</button>
    </div>
  </header>

  <div class="grid">
    <!-- Left Column: Milestone & Feedback Form -->
    <div>
      <div class="card">
        <h2>📦 1. Active Agent Milestone Checkpoint</h2>
        <p class="sub">Agent <strong>Llama-Code-70b</strong> paused at blocking checkpoint.</p>
        <div>
          <strong id="m-title">Loading...</strong>
          <pre id="m-diff">git diff loading...</pre>
        </div>
        <div class="btn-group">
          <button class="btn btn-secondary" onclick="simulateTeam()">👥 Auto-Simulate 3 Teammate Responses</button>
        </div>
      </div>

      <div class="card">
        <h2>📝 2. Stakeholder Question List (Inquiry Experience)</h2>
        <p class="sub">Structured sensemaking questions presented to reviewers.</p>

        <form id="feedback-form" onsubmit="submitForm(event)">
          <div class="form-group">
            <label>Your Name</label>
            <input type="text" id="author_name" value="Alex Rivera" required>
          </div>
          <div class="form-group">
            <label>Functional Role</label>
            <select id="author_role">
              <option value="Engineering">Engineering</option>
              <option value="Product">Product Management</option>
              <option value="Design">UI/UX Design</option>
              <option value="QA/Security">QA / DevOps / Security</option>
            </select>
          </div>

          <div class="form-group">
            <label>Q1: Does this changeset satisfy the accepted requirements? (1 = Disagree, 5 = Agree)</label>
            <div class="scale-options">
              <div class="scale-btn" onclick="selectScale('q1', 1)">1</div>
              <div class="scale-btn" onclick="selectScale('q1', 2)">2</div>
              <div class="scale-btn" onclick="selectScale('q1', 3)">3</div>
              <div class="scale-btn" onclick="selectScale('q1', 4)">4</div>
              <div class="scale-btn active" onclick="selectScale('q1', 5)">5</div>
            </div>
            <input type="hidden" id="q1_val" value="5">
          </div>

          <div class="form-group">
            <label>Q2: Operational Risk Assessment</label>
            <select id="q2_val">
              <option value="Low">Low - Safe to run unmonitored</option>
              <option value="Medium">Medium - Standard deployment review</option>
              <option value="High">High - Requires DB lock inspection</option>
              <option value="Critical">Critical - Halt execution until verified</option>
            </select>
          </div>

          <div class="form-group">
            <label>Q3: Edge Case Completeness</label>
            <select id="q3_val">
              <option value="Yes">Yes - Boundary conditions handled</option>
              <option value="Partially">Partially - Missing failure fallback</option>
              <option value="No">No - High chance of runtime panic</option>
            </select>
          </div>

          <div class="form-group">
            <label>Q4: Constructive Guidance / Critical Note for Agent</label>
            <textarea id="q4_val" rows="3" placeholder="Provide specific adjustment instructions for the agent's next step...">Ensure database migration statement specifies lock_timeout to avoid blocking traffic.</textarea>
          </div>

          <button type="submit" class="btn" style="width: 100%;">Submit Feedback & Trigger Analysis</button>
        </form>
      </div>
    </div>

    <!-- Right Column: Analysis, Polarity Map, Agent Prompt Delta -->
    <div>
      <div class="card">
        <h2>📊 3. Sensemaking Analysis & Polarity Map</h2>
        <p class="sub">Clustered team consensus & divergence analysis.</p>

        <div class="metric-card">
          <div class="metric-box">
            <div style="font-size: 11px; text-transform: uppercase; color: var(--text-muted);">Consensus Index</div>
            <div class="metric-val" id="metric-consensus">--</div>
          </div>
          <div class="metric-box">
            <div style="font-size: 11px; text-transform: uppercase; color: var(--text-muted);">Assessed Risk</div>
            <div class="metric-val" id="metric-risk" style="color: #f59e0b;">--</div>
          </div>
          <div class="metric-box">
            <div style="font-size: 11px; text-transform: uppercase; color: var(--text-muted);">Disposition</div>
            <div class="metric-val" id="metric-disp" style="font-size: 18px; color: #10b981;">--</div>
          </div>
        </div>

        <div id="divergence-section" style="margin-top: 14px;">
          <h3 style="font-size: 13px; color: #cbd5e1; margin-bottom: 8px;">Role Polarization Signals:</h3>
          <div id="div-list" style="font-size: 13px; color: var(--text-muted);">No analysis data yet. Submit feedback or click simulate.</div>
        </div>
      </div>

      <div class="card">
        <h2>🤖 4. Synthesized Context v2 (Fed back to Agent)</h2>
        <p class="sub">The actionable prompt delta generated from team feedback.</p>
        <pre id="agent-prompt-delta" style="min-height: 200px; color: #a5f3fc; white-space: pre-wrap;">Waiting for team feedback to generate prompt delta...</pre>
      </div>
    </div>
  </div>
</div>

<script>
  let activeMilestoneID = "ms-checkout-001";

  // Connect SSE for live notifications
  const evtSource = new EventSource("/events");
  evtSource.addEventListener("notification", function(e) {
    const data = JSON.parse(e.data);
    showToast(data.title, data.message, data.severity);
    loadAnalysis();
  });
  evtSource.onerror = function() {
    document.getElementById("sse-status").innerText = "○ Disconnected";
    document.getElementById("sse-status").style.background = "#7f1d1d";
  };

  function showToast(title, msg, sev="info") {
    const container = document.getElementById("toast-container");
    const toast = document.createElement("div");
    toast.className = "toast " + sev;
    toast.innerHTML = '<div class="toast-title"><span>' + title + '</span><span style="cursor:pointer;" onclick="this.parentElement.parentElement.remove()">✕</span></div><div class="toast-body">' + msg + '</div>';
    container.appendChild(toast);
    setTimeout(() => { toast.remove(); }, 6000);
  }

  function selectScale(name, val) {
    document.getElementById(name + "_val").value = val;
    const btns = event.target.parentElement.querySelectorAll(".scale-btn");
    btns.forEach(b => b.classList.remove("active"));
    event.target.classList.add("active");
  }

  async function loadMilestone() {
    const res = await fetch("/api/milestones/" + activeMilestoneID);
    const m = await res.json();
    document.getElementById("m-title").innerText = m.title;
    document.getElementById("m-diff").innerText = m.diff_snippet;
    loadAnalysis();
  }

  async function loadAnalysis() {
    const res = await fetch("/api/analysis?milestone_id=" + activeMilestoneID);
    const a = await res.json();
    if (!a || a.response_count === 0) return;

    document.getElementById("metric-consensus").innerText = Math.round(a.consensus_index * 100) + "%";
    document.getElementById("metric-risk").innerText = a.overall_risk_tier;
    document.getElementById("metric-disp").innerText = a.disposition.toUpperCase();

    // Divergences
    const divList = document.getElementById("div-list");
    if (a.role_divergences && a.role_divergences.length > 0) {
      divList.innerHTML = a.role_divergences.map(rd => {
        const tag = rd.is_polarized ? '<span class="polar-tag alert">POLARIZED</span>' : '<span class="polar-tag ok">ALIGNED</span>';
        const roleStr = Object.entries(rd.role_means).map(([k,v]) => k + ': ' + v).join(" | ");
        return '<div style="margin-bottom:8px; padding:6px; background:#10131c; border-radius:4px;"><strong>' + rd.prompt + '</strong> ' + tag + '<br><span style="color:#94a3b8; font-size:12px;">' + roleStr + ' (Div: ' + rd.divergence + ')</span></div>';
      }).join("");
    } else {
      divList.innerHTML = "No significant role divergence detected.";
    }

    // Prompt Delta
    document.getElementById("agent-prompt-delta").innerText = a.agent_prompt_delta;
  }

  async function triggerNotify() {
    await fetch("/api/milestones/notify", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ milestone_id: activeMilestoneID })
    });
  }

  async function submitForm(e) {
    e.preventDefault();
    const payload = {
      milestone_id: activeMilestoneID,
      author_name: document.getElementById("author_name").value,
      author_role: document.getElementById("author_role").value,
      answers: [
        { question_id: "q1_intent", value_number: parseFloat(document.getElementById("q1_val").value) },
        { question_id: "q2_risk", value_string: document.getElementById("q2_val").value },
        { question_id: "q3_edge", value_string: document.getElementById("q3_val").value },
        { question_id: "q4_adjust", value_string: document.getElementById("q4_val").value }
      ]
    };
    await fetch("/api/responses", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload)
    });
    loadAnalysis();
  }

  async function simulateTeam() {
    showToast("Simulating", "Injecting responses from Lead Dev, PM, and DevOps...", "info");
    await fetch("/api/simulate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ milestone_id: activeMilestoneID })
    });
    loadAnalysis();
  }

  // Initial load
  loadMilestone();
</script>
</body>
</html>
`
