package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"sync"
)

// Caller is who is calling: the launch whose token came with the
// request.
type Caller struct {
	LaunchID string
	Label    string
	Provider string
}

// Annotations are hints about a tool, as MCP defines them. Clients
// treat them as untrusted; Devcules sets them truthfully.
type Annotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

// Result is what a tool returns. Text is for the model to read;
// Structured, when set, is the same answer as JSON (and must match the
// tool's OutputSchema). IsError marks a failure the model can act on:
// bad arguments, nothing found, a refusal.
type Result struct {
	Text       string
	Structured any
	IsError    bool
}

// Errorf is a tool failure result.
func Errorf(format string, args ...any) Result {
	return Result{Text: fmt.Sprintf(format, args...), IsError: true}
}

// Tool is one MCP tool. Call returns a Go error only for an internal
// failure; the model then sees "internal error", never its text.
type Tool struct {
	Name         string
	Title        string
	Description  string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	Annotations  Annotations
	Call         func(ctx context.Context, c Caller, args json.RawMessage) (Result, error)
}

// toolNameRE is the spec's recommended tool name alphabet.
var toolNameRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// Registry holds the tools, in name order.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Register adds tools. A bad name, a duplicate, a missing handler or a
// schema that is not a JSON object is a wiring bug and an error.
func (r *Registry) Register(tools ...Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range tools {
		if !toolNameRE.MatchString(t.Name) {
			return fmt.Errorf("mcp: tool name %q must be 1 to 128 of A-Z a-z 0-9 _ . -", t.Name)
		}
		if _, dup := r.tools[t.Name]; dup {
			return fmt.Errorf("mcp: tool %q registered twice", t.Name)
		}
		if t.Call == nil || t.Description == "" {
			return fmt.Errorf("mcp: tool %q needs a description and a handler", t.Name)
		}
		if !isObject(t.InputSchema) || (t.OutputSchema != nil && !isObject(t.OutputSchema)) {
			return fmt.Errorf("mcp: tool %q schemas must be JSON objects", t.Name)
		}
		r.tools[t.Name] = t
	}
	return nil
}

func isObject(raw json.RawMessage) bool {
	var m map[string]any
	return json.Unmarshal(raw, &m) == nil && m != nil
}

func (r *Registry) get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// sorted lists the tools in name order: deterministic, as the spec
// asks, so cursors stay stable.
func (r *Registry) sorted() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// toolJSON is a tool as tools/list shows it.
type toolJSON struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  Annotations     `json:"annotations"`
}

func (t Tool) listed() toolJSON {
	return toolJSON{Name: t.Name, Title: t.Title, Description: t.Description,
		InputSchema: t.InputSchema, OutputSchema: t.OutputSchema, Annotations: t.Annotations}
}

// DecodeArgs reads tool arguments into v, refusing unknown fields and
// trailing data. Empty arguments decode as {}.
func DecodeArgs(args json.RawMessage, v any) error {
	if len(bytes.TrimSpace(args)) == 0 || string(bytes.TrimSpace(args)) == "null" {
		args = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing data after the arguments")
	}
	return nil
}
