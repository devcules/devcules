package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/push"
)

// maxHookWait caps one hook long poll. The server's write timeout is
// lifted for it; the hook polls again until the answer comes.
const maxHookWait = 25 * time.Second

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// approvalsRoot answers GET /api/approvals?status=&limit=: approvals
// newest first, and how many are pending (for the nav badge).
func approvalsRoot(svc *approvals.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "approvals not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		list, err := svc.List(r.Context(), r.URL.Query().Get("status"), limit)
		if err != nil {
			if strings.HasPrefix(err.Error(), "approvals: ") {
				writeAuthError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), "approvals: "))
				return
			}
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		pending, err := svc.List(r.Context(), "pending", 200)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Approvals []*approvals.Approval `json:"approvals"`
			Pending   int                   `json:"pending"`
		}{list, len(pending)})
	})
}

// approvalOne serves /api/approvals/{id} (POST {"decision": "allow" |
// "deny"}) and the settings below /api/approvals/: GET settings, PUT
// modes, PUT rules, POST check. Writes carry the CSRF check.
func approvalOne(svc *approvals.Service, runners func() []string, publicURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "approvals not configured")
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/api/approvals/")
		if rest == "settings" {
			if requireMethod(w, r, http.MethodGet) {
				writeJSON(w, http.StatusOK, approvalSettings(svc, runners))
			}
			return
		}
		csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			by, _ := requestUsername(r)
			switch rest {
			case "modes":
				if !requireMethod(w, r, http.MethodPut) {
					return
				}
				var body struct {
					Target string         `json:"target"`
					Mode   approvals.Mode `json:"mode"`
				}
				if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body) != nil {
					writeAuthError(w, http.StatusBadRequest, `body must be {"target": "runner:<name>", "mode": "critical"}`)
					return
				}
				if err := svc.SetMode(r.Context(), body.Target, body.Mode, by, clientIP(r)); err != nil {
					badOrInternal(w, err)
					return
				}
				writeJSON(w, http.StatusOK, approvalSettings(svc, runners))
			case "rules":
				if !requireMethod(w, r, http.MethodPut) {
					return
				}
				var body struct {
					Rules []approvals.Rule `json:"rules"`
				}
				if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body) != nil {
					writeAuthError(w, http.StatusBadRequest, `body must be {"rules": [...]}`)
					return
				}
				if err := svc.SetRules(r.Context(), body.Rules, by, clientIP(r)); err != nil {
					badOrInternal(w, err)
					return
				}
				writeJSON(w, http.StatusOK, approvalSettings(svc, runners))
			case "check":
				if !requireMethod(w, r, http.MethodPost) {
					return
				}
				var body struct {
					Command string `json:"command"`
				}
				if json.NewDecoder(http.MaxBytesReader(w, r.Body, approvals.MaxCommand+1<<10)).Decode(&body) != nil {
					writeAuthError(w, http.StatusBadRequest, `body must be {"command": "..."}`)
					return
				}
				rule, critical := svc.Check(body.Command)
				out := struct {
					Critical bool            `json:"critical"`
					Rule     *approvals.Rule `json:"rule,omitempty"`
				}{Critical: critical}
				if critical {
					out.Rule = &rule
				}
				writeJSON(w, http.StatusOK, out)
			default:
				if !requireMethod(w, r, http.MethodPost) {
					return
				}
				if rest == "" || strings.Contains(rest, "/") {
					writeAuthError(w, http.StatusNotFound, "no such approval")
					return
				}
				var body struct {
					Decision string `json:"decision"`
				}
				if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body) != nil || (body.Decision != "allow" && body.Decision != "deny") {
					writeAuthError(w, http.StatusBadRequest, `body must be {"decision": "allow"} or {"decision": "deny"}`)
					return
				}
				a, err := svc.Answer(r.Context(), rest, body.Decision == "allow", by, clientIP(r))
				switch {
				case errors.Is(err, approvals.ErrNotFound):
					writeAuthError(w, http.StatusNotFound, "no such approval")
				case errors.Is(err, approvals.ErrAnswered):
					writeAuthError(w, http.StatusConflict, "already answered")
				case err != nil:
					writeAuthError(w, http.StatusInternalServerError, "internal error")
				default:
					writeJSON(w, http.StatusOK, a)
				}
			}
		})).ServeHTTP(w, r)
	})
}

func badOrInternal(w http.ResponseWriter, err error) {
	if msg, ok := strings.CutPrefix(err.Error(), "approvals: "); ok {
		writeAuthError(w, http.StatusBadRequest, msg)
		return
	}
	writeAuthError(w, http.StatusInternalServerError, "internal error")
}

// target is one row of the access mode settings.
type target struct {
	Target string         `json:"target"`
	Runner string         `json:"runner"`
	Linked bool           `json:"linked"`
	Mode   approvals.Mode `json:"mode"`
}

// approvalSettings lists every target (launches with no runner, the
// linked runners, and runners that have a mode), the rules and the
// timeout.
func approvalSettings(svc *approvals.Service, runners func() []string) any {
	modes := svc.Modes()
	linked := runners()
	rows := []target{{Target: approvals.Unbound, Mode: modes[approvals.Unbound]}}
	seen := map[string]bool{}
	names := slices.Clone(linked)
	for t := range modes {
		if name, ok := strings.CutPrefix(t, "runner:"); ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		t := approvals.Target(n)
		rows = append(rows, target{Target: t, Runner: n, Linked: slices.Contains(linked, n), Mode: modes[t]})
	}
	return struct {
		Targets        []target         `json:"targets"`
		Modes          []approvals.Mode `json:"modes"`
		Rules          []approvals.Rule `json:"rules"`
		TimeoutSeconds int              `json:"timeoutSeconds"`
	}{rows, approvals.Modes, svc.Rules(), int(svc.Timeout().Seconds())}
}

// launchCaller checks the bearer launch token, like the router: no
// session, no CSRF, since hooks are not browsers.
func launchCaller(tokens *launch.Tokens, w http.ResponseWriter, r *http.Request) (launch.Launch, bool) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	l, err := tokens.Check(r.Context(), strings.TrimSpace(token))
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="devcules"`)
		msg := "missing or invalid launch token"
		switch {
		case errors.Is(err, launch.ErrExpired):
			msg = "launch token expired"
		case errors.Is(err, launch.ErrRevoked):
			msg = "launch token revoked"
		}
		writeAuthError(w, http.StatusUnauthorized, msg)
		return launch.Launch{}, false
	}
	return l, true
}

// hookWait lifts the write deadline for one long poll and returns how
// long to wait (?wait= seconds, at most maxHookWait).
func hookWait(w http.ResponseWriter, r *http.Request) time.Duration {
	wait := maxHookWait
	if s := r.URL.Query().Get("wait"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 && time.Duration(n)*time.Second < wait {
			wait = time.Duration(n) * time.Second
		}
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(wait + 10*time.Second))
	return wait
}

// hookApprovals serves the hook side, authenticated by launch token:
// POST /hook/approvals reports a tool call and, when it must wait,
// long-polls for the answer; GET /hook/approvals/{id} polls again.
// Both answer {"decision": "allow" | "deny" | "ask", "reason", "approval"}.
func hookApprovals(svc *approvals.Service, tokens *launch.Tokens) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if svc == nil || tokens == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "approvals not configured")
			return
		}
		l, ok := launchCaller(tokens, w, r)
		if !ok {
			return
		}
		id, hasID := strings.CutPrefix(r.URL.Path, "/hook/approvals/")
		if !hasID {
			if !requireMethod(w, r, http.MethodPost) {
				return
			}
			var body struct {
				Agent        string `json:"agent"`
				Tool         string `json:"tool"`
				Command      string `json:"command"`
				Irreversible bool   `json:"irreversible"`
			}
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, approvals.MaxCommand+4<<10))
			dec.DisallowUnknownFields()
			if dec.Decode(&body) != nil {
				writeAuthError(w, http.StatusBadRequest, `body must be {"tool": "...", "command": "...", "irreversible": false, "agent": "..."}`)
				return
			}
			out, err := svc.Ask(r.Context(), approvals.Request{LaunchID: l.ID, Runner: l.Runner, Agent: body.Agent,
				Tool: body.Tool, Command: body.Command, Irreversible: body.Irreversible})
			if err != nil {
				badOrInternal(w, err)
				return
			}
			if out.Verdict == approvals.Ask {
				wait := hookWait(w, r)
				reason := out.Reason
				if out, err = svc.Wait(r.Context(), l.ID, out.Approval.ID, wait); err != nil {
					writeAuthError(w, http.StatusInternalServerError, "internal error")
					return
				}
				if out.Verdict == approvals.Ask {
					out.Reason = reason
				}
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		out, err := svc.Wait(r.Context(), l.ID, id, hookWait(w, r))
		switch {
		case errors.Is(err, approvals.ErrNotFound):
			writeAuthError(w, http.StatusNotFound, "no such approval for this launch")
		case err != nil:
			writeAuthError(w, http.StatusInternalServerError, "internal error")
		default:
			writeJSON(w, http.StatusOK, out)
		}
	})
}

// pushRoot answers GET /api/push with the VAPID public key and the
// subscribed devices.
func pushRoot(svc *push.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "push not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		devices, err := svc.Devices(r.Context())
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, struct {
			PublicKey string        `json:"publicKey"`
			Devices   []push.Device `json:"devices"`
		}{svc.PublicKey(), devices})
	})
}

// pushOne serves POST /api/push/devices (subscribe this browser),
// DELETE /api/push/devices/{id} and POST /api/push/test. All carry the
// CSRF check.
func pushOne(svc *push.Service, publicURL string, rec auditor) http.Handler {
	return csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "push not configured")
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/api/push/")
		switch {
		case rest == "devices":
			if !requireMethod(w, r, http.MethodPost) {
				return
			}
			var body struct {
				Endpoint string `json:"endpoint"`
				Keys     struct {
					P256dh string `json:"p256dh"`
					Auth   string `json:"auth"`
				} `json:"keys"`
				Label string `json:"label"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body) != nil {
				writeAuthError(w, http.StatusBadRequest, `body must be the browser's PushSubscription JSON plus "label"`)
				return
			}
			id, err := svc.Subscribe(r.Context(), body.Endpoint, body.Keys.P256dh, body.Keys.Auth, body.Label)
			if err != nil {
				if msg, ok := strings.CutPrefix(err.Error(), "push: "); ok {
					writeAuthError(w, http.StatusBadRequest, msg)
					return
				}
				writeAuthError(w, http.StatusInternalServerError, "internal error")
				return
			}
			rec.record(r, audit.PushSubscribe, id, map[string]string{"label": body.Label})
			writeJSON(w, http.StatusCreated, struct {
				ID string `json:"id"`
			}{id})
		case strings.HasPrefix(rest, "devices/"):
			if !requireMethod(w, r, http.MethodDelete) {
				return
			}
			id := strings.TrimPrefix(rest, "devices/")
			removed, err := svc.Remove(r.Context(), id)
			switch {
			case err != nil:
				writeAuthError(w, http.StatusInternalServerError, "internal error")
			case !removed:
				writeAuthError(w, http.StatusNotFound, "no such device")
			default:
				rec.record(r, audit.PushRemove, id, nil)
				w.WriteHeader(http.StatusNoContent)
			}
		case rest == "test":
			if !requireMethod(w, r, http.MethodPost) {
				return
			}
			res, err := svc.Send(r.Context(), push.Message{Title: "Devcules", Body: "Notifications work on this device.", URL: "/", Tag: "test"})
			if err != nil {
				writeAuthError(w, http.StatusInternalServerError, "internal error")
				return
			}
			writeJSON(w, http.StatusOK, res)
		default:
			writeAuthError(w, http.StatusNotFound, "not found")
		}
	}))
}
