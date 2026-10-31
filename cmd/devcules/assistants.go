package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/devcules/devcules/internal/approvals"
	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/assistants/assistantsopenai"
	builtin "github.com/devcules/devcules/internal/assistants/builtin"
	"github.com/devcules/devcules/internal/assistants/hermes"
	"github.com/devcules/devcules/internal/assistants/openclaw"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/providers"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/vault"
)

// setupAssistants wires the assistant registry serve serves at
// /api/assistants: today the built-in helper on the default profile,
// shadow-read-only when that profile's key is not in the vault. It
// never fails startup: a desk without a working helper model still
// serves the picker so the UI can say so.
func setupAssistants(cfg config.Config, profiles *providers.Service, v *vault.Vault, llm *router.Router,
	mcpHandler http.Handler, launches *launch.Tokens, ap *approvals.Service,
	redactor *redact.Redactor, settings *redact.Settings, auditLog *audit.Log, log *slog.Logger,
) *assistants.Registry {
	opts := assistantSetupOpts(cfg, profiles, v, log)
	opts.MCP, opts.Launches, opts.Decider = mcpHandler, launches, ap
	opts.Redact, opts.Settings = redactor, settings
	opts.OnEvent = assistants.AuditEvents(auditLog, nil)
	a, err := builtin.Builtin(opts)
	if err != nil {
		log.Warn("assistant disabled", "assistant", "builtin", "err", err.Error())
		return emptyAssistants(log)
	}
	list := []assistants.Assistant{a}
	// Style adapters are opt-in: a desk lists one only when a profile
	// named for it exists (base URL and key carry the endpoint). The
	// assistants/ profiles docs pin "hermes" and "openclaw" as the ids.
	for _, w := range []struct {
		id, model string
		build     func(*assistants.Loop, string) (assistants.Assistant, error)
	}{
		{"hermes", hermes.DefaultModel, func(l *assistants.Loop, s string) (assistants.Assistant, error) { return hermes.New(l, s) }},
		{"openclaw", openclaw.DefaultModel, func(l *assistants.Loop, s string) (assistants.Assistant, error) { return openclaw.New(l, s) }},
	} {
		a := styleAdapter(w.id, w.model, profiles, v, mcpHandler, launches, ap, redactor, settings, auditLog, w.build, log)
		if a != nil {
			list = append(list, a)
		}
	}
	reg, err := assistants.NewRegistry(list...)
	if err != nil {
		log.Warn("assistants disabled", "err", err.Error())
		return emptyAssistants(log)
	}
	return reg
}

// styleAdapter wires one opt-in style adapter: its profile holds the
// base URL and model, the vault the key (provider/<id>/api_key). It
// returns nil when the profile is not there, so a desk that never set
// the adapter up simply does not list it.
func styleAdapter(id, model string, profiles *providers.Service, v *vault.Vault,
	mcpHandler http.Handler, launches *launch.Tokens, ap *approvals.Service,
	redactor *redact.Redactor, settings *redact.Settings, _ *audit.Log,
	build func(*assistants.Loop, string) (assistants.Assistant, error), log *slog.Logger,
) assistants.Assistant {
	p, err := profiles.Get(context.Background(), id)
	if err != nil {
		return nil
	}
	if p.Model != "" {
		model = p.Model
	}
	key := ""
	if v != nil {
		if b, err := v.Get(context.Background(), router.KeySecret(id)); err == nil {
			key = string(b)
		}
	}
	caller, err := assistantsopenai.NewCaller(model, p.BaseURL, key, assistantsopenai.NoRedirect(nil))
	if err != nil {
		log.Warn("assistant disabled", "assistant", id, "err", err.Error())
		return nil
	}
	tok := ""
	if launches != nil {
		if t, _, err := launches.Mint(context.Background(), id, "", 10*time.Minute); err == nil {
			tok = t
		}
	}
	runner := builtin.ToolsRunner{MCP: mcpHandler, Token: tok}
	loop, err := assistants.NewLoop(id, assistants.Options{
		Model: caller,
		Tools: runner.Tools,
		Gate: assistants.ApprovalsGate(assistants.PolicyOptions{
			Decider: ap, Runner: "assistant:" + id, Agent: id, LaunchID: "assist:" + id,
		}, runner.Run),
	}, nil)
	if err != nil {
		log.Warn("assistant disabled", "assistant", id, "err", err.Error())
		return nil
	}
	a, err := build(loop, "")
	if err != nil {
		return nil
	}
	log.Info("assistant wired", "assistant", id, "model", model)
	return a
}

// assistantSetupOpts resolves the helper model: the default profile's
// model (falling back to the desk's standard) against its base URL,
// with the vault's key for that profile. A profile without a key
// leaves Key empty: the assistant still loads, and its web surface
// says the helper model could not be reached rather than failing
// serve.
func assistantSetupOpts(cfg config.Config, profiles *providers.Service, v *vault.Vault, log *slog.Logger) builtin.SetupOptions {
	o := builtin.SetupOptions{Model: builtin.DefaultModel}
	if profiles == nil {
		return o
	}
	def, ok := profiles.Default(context.Background())
	if !ok {
		return o
	}
	o.BaseURL = def.BaseURL
	if def.Model != "" {
		o.Model = def.Model
	}
	if cfg.MetaAPIKey != "" {
		o.Key = cfg.MetaAPIKey
	} else if v != nil {
		if key, err := v.Get(context.Background(), router.KeySecret(def.ID)); err == nil {
			o.Key = string(key)
		} else {
			log.Info("assistant has no provider key yet", "provider", def.ID)
		}
	}
	return o
}

// emptyAssistants is the registry with a booked id and no assistant:
// the API lists nothing, and the routes answer 503 cleanly.
func emptyAssistants(log *slog.Logger) *assistants.Registry {
	reg, err := assistants.NewRegistry()
	if err != nil {
		log.Warn("assistants registry", "err", err.Error())
		return nil
	}
	return reg
}
