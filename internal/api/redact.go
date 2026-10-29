package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/devcules/devcules/internal/redact"
)

// maxRedactTestBody caps the test box: it is for pasting a snippet,
// not a file.
const maxRedactTestBody = 64 << 10

// redactState is the GET /api/redact body: the live policy, the
// choices the UI offers, the counters since serve started, and how
// many secret rules are loaded.
type redactState struct {
	Policy redact.Policy                 `json:"policy"`
	Types  []redact.Type                 `json:"types"`
	Modes  []redact.Mode                 `json:"modes"`
	Stats  map[redact.Type]redact.Counts `json:"stats"`
	Rules  int                           `json:"rules"`
}

// redactTestResult is the POST /api/redact/test body. Text is the
// masked text (empty when blocked); findings never carry values.
type redactTestResult struct {
	Text     string           `json:"text"`
	Findings []redact.Finding `json:"findings"`
	Blocked  []redact.Type    `json:"blocked"`
}

// redactRoot answers GET /api/redact with the redaction state.
func redactRoot(r *redact.Redactor, settings *redact.Settings) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r == nil || settings == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "redaction not configured")
			return
		}
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(redactState{
			Policy: settings.Policy(),
			Types:  redact.Types,
			Modes:  []redact.Mode{redact.Off, redact.Log, redact.Mask, redact.Block},
			Stats:  r.Stats(),
			Rules:  r.Rules(),
		})
	})
}

// redactPolicy answers PUT /api/redact/policy: the body is the whole
// policy, a mode for every type. 204 on success; 400 names what is
// wrong.
func redactPolicy(r *redact.Redactor, settings *redact.Settings) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r == nil || settings == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "redaction not configured")
			return
		}
		if !requireMethod(w, req, http.MethodPut) {
			return
		}
		var p redact.Policy
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4<<10)).Decode(&p); err != nil {
			writeAuthError(w, http.StatusBadRequest, "body must be a JSON object of type to mode")
			return
		}
		if err := p.Validate(); err != nil {
			writeAuthError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := settings.SetPolicy(req.Context(), p); err != nil {
			writeAuthError(w, http.StatusInternalServerError, "could not save the policy")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// redactTest answers POST /api/redact/test: it runs {"text": ...}
// under the live policy without touching the counters, so the test
// box shows exactly what a model would receive.
func redactTest(r *redact.Redactor, settings *redact.Settings) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r == nil || settings == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "redaction not configured")
			return
		}
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxRedactTestBody)).Decode(&body); err != nil {
			writeAuthError(w, http.StatusBadRequest, "body must be {\"text\": \"...\"}, at most 64 KiB")
			return
		}
		out, findings, err := r.NewTestSession(settings.Policy()).Redact(body.Text)
		res := redactTestResult{Text: out, Findings: findings, Blocked: []redact.Type{}}
		var blocked *redact.BlockedError
		if errors.As(err, &blocked) {
			res.Blocked = blocked.Types
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})
}
