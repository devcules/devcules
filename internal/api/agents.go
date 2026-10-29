package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/terminals/relay"
)

// agentsRoot serves /api/agents?runner=: GET lists the agent CLIs the
// runner offers (installed or not, with the install command) and the
// runner's access mode; POST {"agent", "model"} starts one in a new
// terminal with a fresh launch token tied to that runner. POST adds
// the CSRF check.
func agentsRoot(rl *relay.Relay, tokens *launch.Tokens, queue *approvals.Service, projects store.ProjectsRepo, providers func() []string, publicURL string, rec auditor) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rl == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "no runner linked")
			return
		}
		switch r.Method {
		case http.MethodGet:
			runner, rerr := rl.PickRunner(linkTarget(r))
			if rerr != nil {
				writeAuthError(w, rerr.Status, rerr.Msg)
				return
			}
			list, rerr := rl.Agents(r.Context(), runner)
			if rerr != nil {
				writeAuthError(w, rerr.Status, rerr.Msg)
				return
			}
			mode := approvals.Mode("")
			if queue != nil {
				mode = queue.Modes()[approvals.Target(runner)]
			}
			writeJSON(w, http.StatusOK, struct {
				Runner string                `json:"runner"`
				Mode   approvals.Mode        `json:"mode"`
				Agents []terminals.AgentInfo `json:"agents"`
			}{runner, mode, list})
		case http.MethodPost:
			csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				startAgent(w, r, rl, tokens, projects, providers, rec)
			})).ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// startAgent mints the launch token, then opens the agent terminal;
// a failed open revokes the token again.
func startAgent(w http.ResponseWriter, r *http.Request, rl *relay.Relay, tokens *launch.Tokens, projects store.ProjectsRepo, providers func() []string, rec auditor) {
	if tokens == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "launch tokens not configured")
		return
	}
	var body struct {
		Agent   string `json:"agent"`
		Model   string `json:"model"`
		Project string `json:"project"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body) != nil ||
		!terminals.ValidAgentID(body.Agent) || (body.Model != "" && !terminals.ValidModel(body.Model)) ||
		(body.Project != "" && !terminals.ValidProject(body.Project)) {
		writeAuthError(w, http.StatusBadRequest, `body must be {"agent": "musecode", "model": "<optional model id>", "project": "<optional project id>"}`)
		return
	}
	// A project picks the runner and the directory: the agent starts
	// in the project's checkout, tagged with it.
	where := linkTarget(r)
	place := relay.Place{}
	if body.Project != "" {
		if projects == nil {
			writeAuthError(w, http.StatusServiceUnavailable, "projects not configured")
			return
		}
		p, err := projects.Get(r.Context(), body.Project)
		if err != nil {
			writeAuthError(w, http.StatusNotFound, "unknown project")
			return
		}
		where = projectTarget(p)
		place = relay.Place{Dir: p.Dir, Project: p.ID}
	}
	runner, rerr := rl.PickRunner(where)
	if rerr != nil {
		writeAuthError(w, rerr.Status, rerr.Msg)
		return
	}
	list, rerr := rl.Agents(r.Context(), runner)
	if rerr != nil {
		writeAuthError(w, rerr.Status, rerr.Msg)
		return
	}
	i := slices.IndexFunc(list, func(a terminals.AgentInfo) bool { return a.ID == body.Agent })
	switch {
	case i < 0:
		writeAuthError(w, http.StatusNotFound, "runner "+runner+" offers no agent "+body.Agent)
		return
	case !list[i].Installed:
		writeAuthError(w, http.StatusConflict, list[i].Name+" is not installed on "+runner+": install it there with "+list[i].Install)
		return
	case !slices.Contains(providers(), list[i].Provider):
		writeAuthError(w, http.StatusConflict, list[i].Name+" needs the "+list[i].Provider+" provider, which the router does not serve")
		return
	}
	info := list[i]
	token, l, err := tokens.MintOn(r.Context(), runner, info.Name+" on "+runner, info.Provider, launch.DefaultTTL)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal error")
		return
	}
	rec.record(r, audit.LaunchMint, l.ID, map[string]string{"label": l.Label, "provider": l.Provider, "runner": runner, "expires": l.Expires.Format(time.RFC3339)})
	id, rerr := rl.OpenAgentAt(r.Context(), runner, terminals.AgentLaunch{ID: info.ID, Token: token, Model: body.Model}, place)
	if rerr != nil {
		// Detached from the request: a client that gave up must not
		// leave a live token behind.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		if tokens.Revoke(ctx, l.ID) == nil {
			rec.record(r, audit.LaunchRevoke, l.ID, map[string]string{"reason": "agent did not start"})
		}
		writeAuthError(w, rerr.Status, rerr.Msg)
		return
	}
	detail := map[string]string{"agent": info.ID, "runner": runner, "launch": l.ID}
	if body.Model != "" {
		detail["model"] = body.Model
	}
	rec.record(r, audit.AgentLaunch, id, detail)
	writeJSON(w, http.StatusCreated, struct {
		ID     string        `json:"id"`
		Launch launch.Launch `json:"launch"`
	}{id, l})
}
