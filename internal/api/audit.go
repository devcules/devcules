package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/trace"
)

// auditor records audit events for the handlers. A nil log records
// nothing, so handlers call it unconditionally.
type auditor struct {
	log *audit.Log
}

// record files an event by the session's user.
func (a auditor) record(r *http.Request, action, target string, detail map[string]string) {
	username, _ := requestUsername(r)
	a.as(r, username, action, target, detail)
}

// as files an event by a named actor: sign-in knows the user before
// any session carries it. Failures are logged by the audit package;
// the action already happened and is not undone.
func (a auditor) as(r *http.Request, actor, action, target string, detail map[string]string) {
	if a.log == nil {
		return
	}
	if actor == "" {
		actor = "anonymous"
	}
	_, _ = a.log.Record(r.Context(), audit.Event{Actor: actor, Action: action, Target: target, IP: clientIP(r), Detail: detail})
}

// signInFailed files a failed sign-in. The typed username is not kept:
// people paste passwords into it.
func (a auditor) signInFailed(r *http.Request, method, reason string) {
	a.as(r, "anonymous", audit.SignInFailed, "", map[string]string{"method": method, "reason": reason})
}

// auditRoot answers GET /api/audit?before=&limit=: entries newest
// first, a page at a time.
func auditRoot(log *audit.Log) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if log == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "audit not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		entries, err := log.Recent(r.Context(), before, limit)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Entries []audit.Entry `json:"entries"`
		}{entries})
	})
}

// auditExport answers GET /api/audit/export with the whole log as
// JSON lines, and GET /api/audit/export?verify=1 with the chain check.
// The export is itself audited, before it is written, so it lists
// itself.
func auditExport(log *audit.Log, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if log == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "audit not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		if r.URL.Query().Get("verify") == "1" {
			n, err := log.Verify(r.Context())
			res := struct {
				OK      bool   `json:"ok"`
				Checked int64  `json:"checked"`
				Broken  int64  `json:"brokenAt,omitempty"`
				Reason  string `json:"reason,omitempty"`
			}{OK: err == nil, Checked: n}
			var be *audit.BrokenError
			switch {
			case errors.As(err, &be):
				res.Broken, res.Reason = be.Seq, be.Reason
			case err != nil:
				writeAuthError(w, http.StatusInternalServerError, "internal error")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(res)
			return
		}
		rec.record(r, audit.AuditExport, "", nil)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", `attachment; filename="devcules-audit-`+time.Now().UTC().Format("2006-01-02")+`.jsonl"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = log.Export(r.Context(), w, 0)
	})
}

// traceSession is a trace summary with its launch's label.
type traceSession struct {
	trace.Session
	Label    string `json:"label"`
	Provider string `json:"provider"`
}

// launchesByID maps launch ids to launches; a nil Tokens maps none.
func launchesByID(r *http.Request, tokens *launch.Tokens) map[string]launch.Launch {
	out := map[string]launch.Launch{}
	if tokens == nil {
		return out
	}
	list, err := tokens.List(r.Context())
	if err != nil {
		return out
	}
	for _, l := range list {
		out[l.ID] = l
	}
	return out
}

// tracesRoot answers GET /api/traces: one summary per launch with
// calls, latest first, labelled with the launch's name.
func tracesRoot(traces *trace.Store, tokens *launch.Tokens) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if traces == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "traces not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		sessions, err := traces.Summaries(r.Context(), 100)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		byID := launchesByID(r, tokens)
		out := make([]traceSession, 0, len(sessions))
		for _, s := range sessions {
			l := byID[s.LaunchID]
			out = append(out, traceSession{Session: s, Label: l.Label, Provider: l.Provider})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Sessions []traceSession `json:"sessions"`
		}{out})
	})
}

// traceOne answers GET /api/traces/{launchID}: that launch's calls,
// oldest first. Reading a trace is audited.
func traceOne(traces *trace.Store, tokens *launch.Tokens, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if traces == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "traces not configured")
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/traces/")
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		calls, err := traces.Calls(r.Context(), id)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		l, known := launchesByID(r, tokens)[id]
		if len(calls) == 0 && !known {
			writeAuthError(w, http.StatusNotFound, "no trace for that launch")
			return
		}
		rec.record(r, audit.TraceRead, id, nil)
		var lp *launch.Launch
		if known {
			lp = &l
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Launch *launch.Launch `json:"launch"`
			Calls  []trace.Call   `json:"calls"`
		}{lp, calls})
	})
}
