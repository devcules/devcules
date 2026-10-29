package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/machines"
	machrelay "github.com/devcules/devcules/internal/machines/relay"
)

// machinesEnrolCode serves POST /api/machines/enrol-code: the owner
// mints a one-time code in the Machines tab and types it on the
// computer (`devcules machine enrol <code>`). The code is shown once;
// the audit entry keeps its label, never the code.
func machinesEnrolCode(iss *machines.Issuer, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if iss == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "machines not configured")
			return
		}
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var body struct {
			Label string `json:"label"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)
		if len(body.Label) > 64 {
			writeAuthError(w, http.StatusBadRequest, "label: at most 64 characters")
			return
		}
		code, err := iss.Mint(r.Context(), body.Label)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "mint failed")
			return
		}
		rec.record(r, audit.MachineEnrolCode, body.Label, nil)
		writeJSON(w, http.StatusCreated, map[string]any{
			"code":      code,
			"expiresIn": int(machines.EnrolTTL / time.Second),
		})
	})
}

// enrolLimiter bounds enrolment attempts per client IP: a code is 60
// bits, so guessing is hopeless already, but PLAN.md asks for rate
// limits and failed tries are cheap to count. Ten tries per ten
// minutes; a success does not reset the window.
type enrolLimiter struct {
	mu    sync.Mutex
	tries map[string][]time.Time
	now   func() time.Time
}

const (
	enrolTries  = 10
	enrolWindow = 10 * time.Minute
)

func newEnrolLimiter() *enrolLimiter {
	return &enrolLimiter{tries: map[string][]time.Time{}, now: time.Now}
}

func (l *enrolLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	kept := l.tries[ip][:0]
	for _, t := range l.tries[ip] {
		if now.Sub(t) < enrolWindow {
			kept = append(kept, t)
		}
	}
	if len(kept) >= enrolTries {
		l.tries[ip] = kept
		return false
	}
	l.tries[ip] = append(kept, now)
	return true
}

// machinesEnrol serves POST /api/machines/enrol: the machine itself
// redeems the code with its public key and a signature over the code,
// proving it holds the private key. No session (a machine is not a
// browser) and no Origin: like /link, any Origin header is refused,
// so no web page can enrol on a visitor's behalf.
func machinesEnrol(iss *machines.Issuer, limit *enrolLimiter, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if iss == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "machines not configured")
			return
		}
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		if r.Header.Get("Origin") != "" {
			writeAuthError(w, http.StatusForbidden, "origin not allowed")
			return
		}
		if !limit.allow(clientIP(r)) {
			writeAuthError(w, http.StatusTooManyRequests, "too many enrolment attempts: wait ten minutes")
			return
		}
		var req struct {
			Code   string `json:"code"`
			Name   string `json:"name"`
			PubKey string `json:"pubkey"`
			Sig    string `json:"sig"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil || req.Code == "" {
			writeAuthError(w, http.StatusBadRequest, "want {code, name, pubkey, sig}")
			return
		}
		pub, err1 := base64.StdEncoding.DecodeString(req.PubKey)
		sig, err2 := base64.StdEncoding.DecodeString(req.Sig)
		if err1 != nil || err2 != nil {
			writeAuthError(w, http.StatusBadRequest, "pubkey and sig: want base64")
			return
		}
		m, err := iss.Redeem(r.Context(), req.Code, pub, sig, req.Name)
		switch {
		case errors.Is(err, machines.ErrBadCode):
			writeAuthError(w, http.StatusBadRequest, "enrolment code unknown, used or expired: mint a new one in the Machines tab")
			return
		case errors.Is(err, machines.ErrNameTaken):
			writeAuthError(w, http.StatusConflict, "a machine with this name is already enrolled: remove it in the Machines tab or pick another --name (the code is spent; mint a new one)")
			return
		case errors.Is(err, machines.ErrBadMachine):
			writeAuthError(w, http.StatusBadRequest, "bad machine name, key or signature")
			return
		case err != nil:
			writeAuthError(w, http.StatusInternalServerError, "enrolment failed")
			return
		}
		rec.as(r, "machine:"+m.Name, audit.MachineEnrol, m.Name, nil)
		writeJSON(w, http.StatusCreated, map[string]string{"name": m.Name})
	})
}

// machineRow is one Machines-tab entry: enrolled machines from the
// store, marked online when their link is up.
type machineRow struct {
	Name       string `json:"name"`
	Online     bool   `json:"online"`
	EnrolledAt string `json:"enrolledAt,omitempty"`
	LastSeen   string `json:"lastSeen,omitempty"`
}

// machinesList serves GET /api/machines: every enrolled machine with
// its link state, plus the mirrored action budget, so the tab shows
// offline machines too (with a way to remove them).
func machinesList(relay *machrelay.Relay, svc *machines.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if relay == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "machines not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		online := map[string]bool{}
		for _, n := range relay.Machines() {
			online[n] = true
		}
		rows := []machineRow{}
		seen := map[string]bool{}
		if svc != nil {
			enrolled, err := svc.List(r.Context())
			if err != nil {
				writeAuthError(w, http.StatusInternalServerError, "machine list failed")
				return
			}
			for _, m := range enrolled {
				row := machineRow{Name: m.Name, Online: online[m.Name], EnrolledAt: m.EnrolledAt.UTC().Format(time.RFC3339)}
				if !m.LastSeen.IsZero() {
					row.LastSeen = m.LastSeen.UTC().Format(time.RFC3339)
				}
				rows = append(rows, row)
				seen[m.Name] = true
			}
		}
		for n := range online {
			if !seen[n] {
				rows = append(rows, machineRow{Name: n, Online: true})
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		writeJSON(w, http.StatusOK, struct {
			Machines []machineRow `json:"machines"`
			Budget   int          `json:"budget"`
		}{Machines: rows, Budget: relay.BudgetRemaining()})
	})
}

// machineOne serves DELETE /api/machines/{name}: revoke the machine.
// Its key stops verifying and its live link is cut, so it has to
// enrol again to come back.
func machineOne(svc *machines.Service, kick func(name string), rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if svc == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "machines not configured")
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/machines/")
		if !machines.ValidName(name) {
			writeAuthError(w, http.StatusNotFound, "unknown machine")
			return
		}
		if !requireMethod(w, r, http.MethodDelete) {
			return
		}
		if err := svc.Remove(r.Context(), name); err != nil {
			writeAuthError(w, http.StatusInternalServerError, "remove failed")
			return
		}
		kick(name)
		rec.record(r, audit.MachineRemove, name, nil)
		w.WriteHeader(http.StatusNoContent)
	})
}
