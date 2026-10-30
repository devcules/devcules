package api

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/providers"
	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/terminals/relay"
)

// agentsRoot serves /api/agents?runner=: GET lists the agent CLIs the
// runner offers (installed or not, with the install command) and the
// runner's access mode; POST {"agent", "model"} starts one in a new
// terminal with a fresh launch token tied to that runner. POST adds
// the CSRF check.
func agentsRoot(rl *relay.Relay, tokens *launch.Tokens, queue *approvals.Service, projects store.ProjectsRepo, rt *router.Router, profiles *providers.Service, publicURL string, rec auditor) http.Handler {
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
			// Each agent lists the profiles it can use now.
			for i := range list {
				list[i].Providers = offers(r.Context(), list[i], rt, profiles)
			}
			writeJSON(w, http.StatusOK, struct {
				Runner string                `json:"runner"`
				Mode   approvals.Mode        `json:"mode"`
				Agents []terminals.AgentInfo `json:"agents"`
			}{runner, mode, list})
		case http.MethodPost:
			csrf(publicURL, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				startAgent(w, r, rl, tokens, projects, rt, profiles, rec)
			})).ServeHTTP(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// startAgent mints the launch token, then opens the agent terminal;
// a failed open revokes the token again.
func startAgent(w http.ResponseWriter, r *http.Request, rl *relay.Relay, tokens *launch.Tokens, projects store.ProjectsRepo, rt *router.Router, profiles *providers.Service, rec auditor) {
	if tokens == nil {
		writeAuthError(w, http.StatusServiceUnavailable, "launch tokens not configured")
		return
	}
	var body struct {
		Agent    string `json:"agent"`
		Model    string `json:"model"`
		Project  string `json:"project"`
		Provider string `json:"provider"`
		Name     string `json:"name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body) != nil ||
		!terminals.ValidAgentID(body.Agent) || (body.Model != "" && !terminals.ValidModel(body.Model)) ||
		(body.Project != "" && !terminals.ValidProject(body.Project)) ||
		(body.Provider != "" && !terminals.ValidAgentID(body.Provider)) ||
		(body.Name != "" && !terminals.ValidName(body.Name)) {
		writeAuthError(w, http.StatusBadRequest, `body must be {"agent": "musecode", "model", "project", "provider", "name": all but agent optional}`)
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
	if i < 0 {
		writeAuthError(w, http.StatusNotFound, "runner "+runner+" offers no agent "+body.Agent)
		return
	}
	info := list[i]
	// The provider: the one picked, else the agent's own when it can
	// use it, else the first it can (the default comes first).
	speaks := offers(r.Context(), info, rt, profiles)
	provider := body.Provider
	if provider == "" {
		provider = info.Provider
		if !slices.Contains(speaks, provider) && len(speaks) > 0 {
			provider = speaks[0]
		}
	}
	model := body.Model
	if model == "" && profiles != nil {
		if p, err := profiles.Get(r.Context(), provider); err == nil {
			model = p.Model
		}
	}
	switch {
	case !info.Installed:
		writeAuthError(w, http.StatusConflict, info.Name+" is not installed on "+runner+": install it there with "+info.Install)
		return
	case !slices.Contains(speaks, provider):
		served := rt != nil && rt.Serves(r.Context(), provider)
		switch {
		case !served && body.Provider == "":
			writeAuthError(w, http.StatusConflict, info.Name+" needs the "+provider+" provider, which the router does not serve: add one it can use in Settings → Providers")
		case !served:
			writeAuthError(w, http.StatusBadRequest, "no provider "+provider+": add it in Settings → Providers")
		default:
			writeAuthError(w, http.StatusBadRequest, info.Name+" cannot use the "+provider+" provider ("+wireNeed(info)+")")
		}
		return
	case !ready(r.Context(), rt, provider):
		writeAuthError(w, http.StatusConflict, "the "+provider+" provider has no API key yet: add its key in Settings")
		return
	case provider != info.Provider && model == "":
		// Another vendor's default model name would not exist there.
		writeAuthError(w, http.StatusBadRequest, "pick a model: "+info.Name+" on "+provider+" has no default model (or set one on the provider in Settings)")
		return
	}
	place.Name = body.Name
	token, l, err := tokens.MintOn(r.Context(), runner, info.Name+" on "+runner, provider, launch.DefaultTTL)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "internal error")
		return
	}
	rec.record(r, audit.LaunchMint, l.ID, map[string]string{"label": l.Label, "provider": l.Provider, "runner": runner, "expires": l.Expires.Format(time.RFC3339)})
	id, rerr := rl.OpenAgentAt(r.Context(), runner, terminals.AgentLaunch{ID: info.ID, Token: token, Model: model, Provider: provider}, place)
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
	detail := map[string]string{"agent": info.ID, "runner": runner, "launch": l.ID, "provider": provider}
	if model != "" {
		detail["model"] = model
	}
	rec.record(r, audit.AgentLaunch, id, detail)
	writeJSON(w, http.StatusCreated, struct {
		ID     string        `json:"id"`
		Launch launch.Launch `json:"launch"`
	}{id, l})
}

// ready reports whether provider id has its key saved.
func ready(ctx context.Context, rt *router.Router, id string) bool {
	ok, _ := rt.Ready(ctx, id)
	return ok
}

// wireNeed says which providers an agent can use, for a refusal.
func wireNeed(info terminals.AgentInfo) string {
	switch {
	case len(info.Only) > 0:
		return "it only speaks to " + strings.Join(info.Only, ", ")
	case info.Wire == terminals.WireMessages:
		return "it needs an Anthropic-format provider, or Meta"
	case info.Wire == terminals.WireResponses:
		return "it needs an OpenAI-format provider that serves /v1/responses"
	}
	return "it speaks " + strings.Join(info.Providers, ", ")
}
