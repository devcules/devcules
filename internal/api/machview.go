package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/devcules/devcules/internal/machframes"
	"github.com/devcules/devcules/internal/machines/elements"
	"github.com/devcules/devcules/internal/machines/ground"
	machrelay "github.com/devcules/devcules/internal/machines/relay"
)

// machinesList serves GET /api/machines: the linked machine names
// and the mirrored action budget, so the UI can offer the task view
// and show what is left before it takes control.
func machinesList(relay *machrelay.Relay) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if relay == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "machines not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		names := relay.Machines()
		if names == nil {
			names = []string{}
		}
		writeJSON(w, http.StatusOK, struct {
			Machines []string `json:"machines"`
			Budget   int      `json:"budget"`
		}{Machines: names, Budget: relay.BudgetRemaining()})
	})
}

// machinesBudget serves GET /api/machines-budget: the mirrored
// remaining actions for the UI's counter. Serve tracks what it has
// sent since the last reset; the machine's own counter stays the
// authority and refuses input at zero whatever serve believes, so a
// serve restart losing the mirror costs a display at most.
func machinesBudget(relay *machrelay.Relay) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if relay == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "machines not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Remaining int `json:"remaining"`
		}{Remaining: relay.BudgetRemaining()})
	})
}

// machinesScreen serves GET /api/machines-screen?machine=: one PNG
// capture from the linked machine.
func machinesScreen(relay *machrelay.Relay) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if relay == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "machines not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		png, aerr := relay.Screen(r.Context(), r.URL.Query().Get("machine"))
		if aerr != nil {
			writeAuthError(w, aerr.Status, aerr.Msg)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(png)
	})
}

// machinesTree serves GET /api/machines-tree?machine=: the machine's
// accessibility tree as JSON.
func machinesTree(relay *machrelay.Relay) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if relay == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "machines not configured")
			return
		}
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		tree, aerr := relay.Tree(r.Context(), r.URL.Query().Get("machine"))
		if aerr != nil {
			writeAuthError(w, aerr.Status, aerr.Msg)
			return
		}
		writeJSON(w, http.StatusOK, tree)
	})
}

// machinesInput serves POST /api/machines-input {kind,x,y,key,text}:
// one injected input event — the take-control call. The action
// budget is spent at the machine, which refuses at zero; a refusal
// comes back as the machine's own reason.
func machinesInput(relay *machrelay.Relay, publicURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if relay == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "machines not configured")
			return
		}
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var ev machframes.Input
			if err := json.NewDecoder(r.Body).Decode(&ev); err != nil || ev.Kind == "" {
				writeAuthError(w, http.StatusBadRequest, "kind, x, y, key and text: kind is required")
				return
			}
			if aerr := relay.Inject(r.Context(), r.URL.Query().Get("machine"), ev); aerr != nil {
				writeAuthError(w, aerr.Status, aerr.Msg)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})).ServeHTTP(w, r)
	})
}

// machinesGround serves POST /api/machines-ground {phrase}: SAM
// matches for the phrase against the machine's live screen, with
// their click points.
func machinesGround(relay *machrelay.Relay, publicURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if relay == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "machines not configured")
			return
		}
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Phrase string `json:"phrase"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Phrase == "" {
				writeAuthError(w, http.StatusBadRequest, "phrase is required")
				return
			}
			matches, aerr := relay.Ground(r.Context(), r.URL.Query().Get("machine"), body.Phrase)
			if aerr != nil {
				writeAuthError(w, aerr.Status, aerr.Msg)
				return
			}
			writeJSON(w, http.StatusOK, struct {
				Matches []ground.Match `json:"matches"`
			}{Matches: matches})
		})).ServeHTTP(w, r)
	})
}

// machinesVerify serves POST /api/machines-verify: the done-verifier's
// fresh look — one capture plus one tree read with the machine's
// note, so a task's success is judged on the live screen.
func machinesVerify(relay *machrelay.Relay, publicURL string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if relay == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "machines not configured")
			return
		}
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			png, tree, note, aerr := relay.Verify(r.Context(), r.URL.Query().Get("machine"))
			if aerr != nil {
				writeAuthError(w, aerr.Status, aerr.Msg)
				return
			}
			writeJSON(w, http.StatusOK, struct {
				PNG  string        `json:"png"`
				Tree elements.Tree `json:"tree"`
				Note string        `json:"note"`
			}{
				PNG:  base64.StdEncoding.EncodeToString(png),
				Tree: tree,
				Note: note,
			})
		})).ServeHTTP(w, r)
	})
}
