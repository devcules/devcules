package registry

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// DefaultInspectTimeout bounds how long a dry-run inspection may run before
// the untrusted process is killed and inspection reported as inconclusive.
const DefaultInspectTimeout = 10 * time.Second

// MCPInspector previews an MCP server by actually running it (the only way
// to discover its real tool list -- nothing in the registry declares tools
// statically) and performing the minimal MCP handshake over stdio:
// initialize -> notifications/initialized -> tools/list. This DOES execute
// the entry's untrusted command; callers must disclose that before calling.
//
// The handshake itself lives in mcp_client.go and is shared with discovery
// and tool invocation -- one MCP client, not three.
type MCPInspector struct {
	Timeout time.Duration // zero => DefaultInspectTimeout
}

func (ins *MCPInspector) Kind() ArtifactKind { return KindMCP }

func (ins *MCPInspector) timeout() time.Duration {
	if ins.Timeout > 0 {
		return ins.Timeout
	}
	return DefaultInspectTimeout
}

// shellQuote/shellQuoteArgs: POSIX single-quote escaping for building the
// -c string passed to `bash -ic`. There's no argv-based alternative here
// (bash -c takes one command string), same reasoning as assistants.go's
// copy of this for tmux send-keys.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func shellQuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

func (ins *MCPInspector) Inspect(ctx context.Context, entry CatalogEntry) (Inspection, error) {
	spec, ok := entry.Install.(MCPInstallSpec)
	if !ok {
		return Inspection{}, fmt.Errorf("registry: entry has no MCP install spec")
	}
	if spec.Type != "stdio" {
		return Inspection{
			Summary: "This is a remote (" + spec.Type + ") server at " + spec.URL + " -- Devcules can only dry-run local stdio servers, not connect to a remote endpoint on your behalf.",
			Warning: "remote server: not inspected",
		}, nil
	}
	return inspectStdio(ctx, spec.Command, spec.Args, ins.timeout())
}

// inspectStdio never hard-errors: "can't preview" shouldn't block browsing
// the rest of the catalog, so every failure becomes a Warning instead.
func inspectStdio(ctx context.Context, command string, args []string, timeout time.Duration) (Inspection, error) {
	sess, err := openMCPSession(ctx, MCPInstallSpec{Type: "stdio", Command: command, Args: args}, timeout)
	if err != nil {
		return Inspection{Warning: cleanInspectErr(err)}, nil
	}
	defer sess.Close()

	tools, err := listMCPTools(ctx, sess)
	if err != nil {
		return Inspection{Warning: cleanInspectErr(err)}, nil
	}
	infos := make([]ToolInfo, 0, len(tools))
	for _, t := range tools {
		infos = append(infos, ToolInfo{Name: t.Name, Description: t.Description})
	}
	return Inspection{
		Summary: fmt.Sprintf("Connected successfully — exposes %d tool(s).", len(infos)),
		Tools:   infos,
	}, nil
}

// cleanInspectErr turns a client error into user-facing prose, keeping the
// hint that a silent server usually just needs credentials to start.
func cleanInspectErr(err error) string {
	msg := strings.TrimPrefix(err.Error(), "mcp: ")
	if strings.Contains(msg, "initialize failed") {
		return "no response to initialize -- may need configuration (env vars/credentials) to start: " + msg
	}
	return msg
}
