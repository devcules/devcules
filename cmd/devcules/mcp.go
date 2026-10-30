package main

import (
	"context"
	"log/slog"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/config"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/mcp"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/router"
	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/terminals/relay"
	"github.com/devcules/devcules/internal/trace"
)

// mcpInstructions is what MCP clients show the model about Devcules.
const mcpInstructions = "Devcules runs this agent. Its tools show the Devcules status, " +
	"this launch, this launch's own model calls (redacted), the open terminals, " +
	"and what redaction would do to a piece of text. All of them only read."

// setupMCP builds the MCP server with the built-in tools. Later
// milestones register their own tools on the same registry.
func setupMCP(cfg config.Config, h *hub.Hub, terms *relay.Relay, llm *router.Router, launches *launch.Tokens,
	traces *trace.Store, auditLog *audit.Log, redactor *redact.Redactor, settings *redact.Settings, log *slog.Logger) (*mcp.Server, error) {
	reg := mcp.NewRegistry()
	err := reg.Register(mcp.Builtins(mcp.BuiltinOptions{
		Version:    version,
		Runners:    h.Runners,
		Providers:  func() []string { return llm.Providers(context.Background()) },
		Launches:   launches,
		TraceStore: traces,
		Terminals: func(ctx context.Context, runner string) ([]terminals.TermInfo, error) {
			// A nil *relay.Error must not become a non-nil error.
			list, rerr := terms.List(ctx, runner)
			if rerr != nil {
				return nil, rerr
			}
			return list, nil
		},
		Redact:         redactor,
		RedactSettings: settings,
	})...)
	if err != nil {
		return nil, err
	}
	return mcp.New(mcp.Options{
		Tokens:       launches,
		Tools:        reg,
		AuditLog:     auditLog,
		PublicURL:    cfg.PublicURL,
		Version:      version,
		Instructions: mcpInstructions,
		Log:          log,
	})
}
