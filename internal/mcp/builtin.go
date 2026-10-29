package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/trace"
)

// maxCheckText caps check_redaction's text, like the settings test box.
const maxCheckText = 64 << 10

// BuiltinOptions are what the built-in tools read. Any nil dependency
// makes its tool answer "not configured" instead of failing to load.
type BuiltinOptions struct {
	Version    string
	Runners    func() []string
	Providers  func() []string
	Launches   *launch.Tokens
	TraceStore *trace.Store
	// Terminals lists a runner's terminals ("" picks the only one).
	Terminals      func(ctx context.Context, runner string) ([]terminals.TermInfo, error)
	Redact         *redact.Redactor
	RedactSettings *redact.Settings
}

// readOnly is every built-in's annotation: they look, never touch.
func readOnly(title string) Annotations {
	return Annotations{Title: title, ReadOnlyHint: true, IdempotentHint: true}
}

var noArgs = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

// Builtins returns the read-only tools every serve offers.
func Builtins(o BuiltinOptions) []Tool {
	notConfigured := func(what string) Result { return Errorf("%s is not configured on this serve", what) }
	return []Tool{
		{
			Name:        "status",
			Title:       "Devcules status",
			Description: "Shows the Devcules version, the linked runners, the model providers the router serves, and the redaction policy in force.",
			InputSchema: noArgs,
			Annotations: readOnly("Devcules status"),
			Call: func(ctx context.Context, c Caller, args json.RawMessage) (Result, error) {
				if err := DecodeArgs(args, &struct{}{}); err != nil {
					return Errorf("status takes no arguments: %v", err), nil
				}
				out := map[string]any{"version": o.Version, "runners": list(o.Runners), "providers": list(o.Providers)}
				if o.RedactSettings != nil {
					out["redaction"] = o.RedactSettings.Policy()
				}
				return Result{Structured: out}, nil
			},
		},
		{
			Name:        "launch_info",
			Title:       "This launch",
			Description: "Shows the launch this agent runs under: its label, the provider its token may call, and when the token expires.",
			InputSchema: noArgs,
			Annotations: readOnly("This launch"),
			Call: func(ctx context.Context, c Caller, args json.RawMessage) (Result, error) {
				if err := DecodeArgs(args, &struct{}{}); err != nil {
					return Errorf("launch_info takes no arguments: %v", err), nil
				}
				if o.Launches == nil {
					return notConfigured("launch tokens"), nil
				}
				all, err := o.Launches.List(ctx)
				if err != nil {
					return Result{}, err
				}
				for _, l := range all {
					if l.ID == c.LaunchID {
						return Result{Structured: l}, nil
					}
				}
				return Errorf("this launch is no longer listed"), nil
			},
		},
		{
			Name:        "my_trace",
			Title:       "My model calls",
			Description: "Shows this launch's own model calls through the router, latest last: model, status, token counts and what was redacted. Text is shown as redacted, with placeholders. It never shows other launches.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":100,"description":"How many of the latest calls to show (default 20)."}},"additionalProperties":false}`),
			Annotations: readOnly("My model calls"),
			Call: func(ctx context.Context, c Caller, args json.RawMessage) (Result, error) {
				var in struct {
					Limit int `json:"limit"`
				}
				if err := DecodeArgs(args, &in); err != nil || in.Limit < 0 || in.Limit > 100 {
					return Errorf("my_trace takes {\"limit\": 1 to 100}"), nil
				}
				if in.Limit == 0 {
					in.Limit = 20
				}
				if o.TraceStore == nil {
					return notConfigured("tracing"), nil
				}
				calls, err := o.TraceStore.Calls(ctx, c.LaunchID)
				if err != nil {
					return Result{}, err
				}
				total := len(calls)
				calls = calls[max(0, total-in.Limit):]
				return Result{Structured: map[string]any{"launchId": c.LaunchID, "total": total, "calls": calls}}, nil
			},
		},
		{
			Name:        "list_terminals",
			Title:       "Terminals",
			Description: "Lists the terminals open on a runner (tmux sessions the user opened in Devcules), with when each was created.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"runner":{"type":"string","description":"The runner's name; leave it out when only one is linked."}},"additionalProperties":false}`),
			Annotations: readOnly("Terminals"),
			Call: func(ctx context.Context, c Caller, args json.RawMessage) (Result, error) {
				var in struct {
					Runner string `json:"runner"`
				}
				if err := DecodeArgs(args, &in); err != nil {
					return Errorf("list_terminals takes {\"runner\": name}"), nil
				}
				if o.Terminals == nil {
					return notConfigured("the terminal relay"), nil
				}
				list, err := o.Terminals(ctx, in.Runner)
				if err != nil {
					// The relay's messages are written for people and
					// hold no secrets: the model can act on them.
					return Errorf("%s", err.Error()), nil
				}
				if list == nil {
					list = []terminals.TermInfo{}
				}
				return Result{Structured: map[string]any{"terminals": list}}, nil
			},
		},
		{
			Name:        "check_redaction",
			Title:       "Check redaction",
			Description: "Runs text through the redaction policy in force without sending it anywhere, and shows what a model call would carry: the text with placeholders, what was found, and whether the call would be blocked. Use it before pasting something that may hold a secret.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","maxLength":65536,"description":"The text to check."}},"required":["text"],"additionalProperties":false}`),
			Annotations: readOnly("Check redaction"),
			Call: func(ctx context.Context, c Caller, args json.RawMessage) (Result, error) {
				var in struct {
					Text *string `json:"text"`
				}
				if err := DecodeArgs(args, &in); err != nil || in.Text == nil {
					return Errorf("check_redaction takes {\"text\": \"...\"}"), nil
				}
				if len(*in.Text) > maxCheckText {
					return Errorf("text is over 64 KiB: check it in parts"), nil
				}
				if o.Redact == nil || o.RedactSettings == nil {
					return notConfigured("redaction"), nil
				}
				// A test session: the check never moves the counters.
				out, findings, err := o.Redact.NewTestSession(o.RedactSettings.Policy()).Redact(*in.Text)
				res := map[string]any{"text": out, "findings": findings, "blocked": []redact.Type{}}
				var blocked *redact.BlockedError
				if errors.As(err, &blocked) {
					res["blocked"] = blocked.Types
				} else if err != nil {
					return Result{}, err
				}
				return Result{Structured: res}, nil
			},
		},
	}
}

// list calls f, sorted, and never nil.
func list(f func() []string) []string {
	if f == nil {
		return []string{}
	}
	out := slices.Clone(f())
	if out == nil {
		out = []string{}
	}
	slices.Sort(out)
	return out
}
