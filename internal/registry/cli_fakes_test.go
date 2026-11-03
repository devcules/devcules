package registry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCLI writes a shell script that logs its arguments, prints out and
// exits with code. It stands in for the claude, codex and skills CLIs so
// the installers' command lines are checked without the real tools.
func fakeCLI(t *testing.T, out string, code int) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls.log")
	outFile := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(outFile, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "cli")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\ncat '" + outFile + "'\nexit " + string(rune('0'+code)) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func readLog(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCodexInstallerDrivesCLI(t *testing.T) {
	ctx := context.Background()
	bin, log := fakeCLI(t, "", 0)
	in := &CodexMCPInstaller{CodexBin: bin}
	if in.Kind() != KindMCP {
		t.Fatal("kind")
	}
	http := CatalogEntry{Install: MCPInstallSpec{Type: "http", URL: "https://mcp.example.com/mcp"}}
	if err := in.Install(ctx, " remote ", http, map[string]string{"bearer_token_env_var": "MCP_TOKEN"}); err != nil {
		t.Fatal(err)
	}
	if err := in.Install(ctx, "remote2", http, map[string]string{"BEARER_TOKEN_ENV_VAR": "OTHER"}); err != nil {
		t.Fatal(err)
	}
	stdio := CatalogEntry{Install: MCPInstallSpec{
		Type: "stdio", Command: "npx", Args: []string{"-y", "srv"},
		EnvVars: []MCPEnvVarHint{{Name: "API_URL"}, {Name: "UNSET"}},
	}}
	if err := in.Install(ctx, "local", stdio, map[string]string{"API_URL": "https://x.example"}); err != nil {
		t.Fatal(err)
	}
	if err := in.Remove(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	got := readLog(t, log)
	for _, want := range []string{
		"mcp add remote --url https://mcp.example.com/mcp --bearer-token-env-var MCP_TOKEN",
		"mcp add remote2 --url https://mcp.example.com/mcp --bearer-token-env-var OTHER",
		"mcp add local -- npx -y srv --env API_URL=https://x.example",
		"mcp remove local",
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("calls missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "UNSET") {
		t.Errorf("an empty env var was passed:\n%s", got)
	}

	for _, c := range []struct {
		name  string
		entry CatalogEntry
	}{
		{"", http},
		{"x", CatalogEntry{Install: "nope"}},
		{"x", CatalogEntry{Install: MCPInstallSpec{Type: "ws"}}},
	} {
		if err := in.Install(ctx, c.name, c.entry, nil); err == nil {
			t.Errorf("Install(%q, %+v) accepted", c.name, c.entry)
		}
	}
	if err := in.Remove(ctx, " "); err == nil {
		t.Error("empty remove accepted")
	}
}

func TestCodexInstallerFailures(t *testing.T) {
	ctx := context.Background()
	bin, _ := fakeCLI(t, "boom: config locked", 1)
	in := &CodexMCPInstaller{CodexBin: bin}
	entry := CatalogEntry{Install: MCPInstallSpec{Type: "sse", URL: "https://x.example"}}
	if err := in.Install(ctx, "x", entry, nil); err == nil || !strings.Contains(err.Error(), "config locked") {
		t.Fatalf("install err = %v", err)
	}
	if err := in.Remove(ctx, "x"); err == nil || !strings.Contains(err.Error(), "config locked") {
		t.Fatalf("remove err = %v", err)
	}
	if _, err := in.Installed(ctx); err == nil {
		t.Fatal("list failure hidden while codex is present")
	}
	gone, _ := fakeCLI(t, "Error: server 'x' not found", 1)
	if err := (&CodexMCPInstaller{CodexBin: gone}).Remove(ctx, "x"); err != nil {
		t.Fatalf("removing a missing server = %v, want success", err)
	}
	junk, _ := fakeCLI(t, "not json", 0)
	if _, err := (&CodexMCPInstaller{CodexBin: junk}).Installed(ctx); err == nil {
		t.Fatal("junk list accepted")
	}
	items, err := (&CodexMCPInstaller{CodexBin: filepath.Join(t.TempDir(), "no-codex")}).Installed(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("codex missing = %+v, %v", items, err)
	}
	if (&CodexMCPInstaller{}).bin() != "codex" {
		t.Error("default bin")
	}
}

func TestCodexInstalledParsesList(t *testing.T) {
	list := `[{"name":"zeta","enabled":true,"transport":{"type":"streamable_http","url":"https://z.example/mcp"}},
{"name":"alpha","enabled":true,"transport":{"type":"stdio","command":"npx","args":["-y","a"]}}]`
	bin, _ := fakeCLI(t, list, 0)
	items, err := (&CodexMCPInstaller{CodexBin: bin}).Installed(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Name != "alpha" || items[0].Detail != "npx -y a" || items[1].Detail != "https://z.example/mcp" || items[1].Agents[0] != "codex" {
		t.Fatalf("items = %+v", items)
	}
}

func TestClaudeInstallerDrivesCLI(t *testing.T) {
	ctx := context.Background()
	bin, log := fakeCLI(t, "", 0)
	in := &MCPInstaller{ClaudeBin: bin, Scope: "project"}
	stdio := CatalogEntry{Install: MCPInstallSpec{
		Type: "stdio", Command: "npx", Args: []string{"srv"},
		EnvVars: []MCPEnvVarHint{{Name: "REGION", Default: "eu"}, {Name: "LEVEL"}},
	}}
	if err := in.Install(ctx, "local", stdio, map[string]string{"LEVEL": "2", "EXTRA": "x"}); err != nil {
		t.Fatal(err)
	}
	if err := in.Install(ctx, "remote", CatalogEntry{Install: MCPInstallSpec{Type: "http", URL: "https://r.example"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := in.Remove(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	got := readLog(t, log)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "mcp add-json local ") || !strings.HasSuffix(lines[0], " -s project") {
		t.Fatalf("calls = %q", lines)
	}
	cfgText := strings.TrimSuffix(strings.TrimPrefix(lines[0], "mcp add-json local "), " -s project")
	var cfg struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(cfgText), &cfg); err != nil {
		t.Fatalf("config %q: %v", cfgText, err)
	}
	if cfg.Env["REGION"] != "eu" || cfg.Env["LEVEL"] != "2" || cfg.Env["EXTRA"] != "x" {
		t.Fatalf("env = %+v", cfg.Env)
	}
	if !strings.Contains(lines[1], `"url":"https://r.example"`) || lines[2] != "mcp remove local -s project" {
		t.Fatalf("calls = %q", lines)
	}
	if err := in.Install(ctx, "x", CatalogEntry{Install: MCPInstallSpec{Type: "ws"}}, nil); err == nil {
		t.Error("unknown type accepted")
	}
	if err := in.Remove(ctx, ""); err == nil {
		t.Error("empty remove accepted")
	}
	if (&MCPInstaller{}).bin() != "claude" || (&MCPInstaller{}).scope() != "user" {
		t.Error("defaults")
	}

	failing, _ := fakeCLI(t, "nope", 1)
	bad := &MCPInstaller{ClaudeBin: failing}
	if err := bad.Install(ctx, "x", CatalogEntry{Install: MCPInstallSpec{Type: "sse", URL: "https://s.example"}}, nil); err == nil {
		t.Error("install failure hidden")
	}
	if err := bad.Remove(ctx, "x"); err == nil {
		t.Error("remove failure hidden")
	}
}

func TestClaudeInstalledReadsConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	in := &MCPInstaller{}
	items, err := in.Installed(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("no config = %+v, %v", items, err)
	}
	doc := `{"mcpServers":{"b":{"url":"https://b.example"},"a":{"command":"npx"}},"other":1}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	items, err = in.Installed(context.Background())
	if err != nil || len(items) != 2 || items[0].Name != "a" || items[0].Detail != "npx" || items[1].Detail != "https://b.example" {
		t.Fatalf("items = %+v, %v", items, err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Installed(context.Background()); err == nil {
		t.Fatal("broken config accepted")
	}
}

func TestTargetedInstallRoutesByHarness(t *testing.T) {
	ctx := context.Background()
	claude, claudeLog := fakeCLI(t, "", 0)
	codex, codexLog := fakeCLI(t, "", 0)
	entry := CatalogEntry{Install: MCPInstallSpec{Type: "http", URL: "https://x.example"}}
	if err := InstallMCPTargeted(ctx, "claude-code", "a", entry, nil, claude, codex); err != nil {
		t.Fatal(err)
	}
	if err := InstallMCPTargeted(ctx, "codex", "b", entry, nil, claude, codex); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMCPTargeted(ctx, "claude", "a", claude, codex); err != nil {
		t.Fatal(err)
	}
	if err := RemoveMCPTargeted(ctx, "codex", "b", claude, codex); err != nil {
		t.Fatal(err)
	}
	if c := readLog(t, claudeLog); !strings.Contains(c, "add-json a") || !strings.Contains(c, "remove a") {
		t.Errorf("claude calls = %s", c)
	}
	if c := readLog(t, codexLog); !strings.Contains(c, "mcp add b") || !strings.Contains(c, "mcp remove b") {
		t.Errorf("codex calls = %s", c)
	}
	if err := InstallMCPTargeted(ctx, "vim", "a", entry, nil, claude, codex); err == nil {
		t.Error("unknown harness install accepted")
	}
	if err := RemoveMCPTargeted(ctx, "vim", "a", claude, codex); err == nil {
		t.Error("unknown harness remove accepted")
	}
}

func TestSkillsInstallerErrorsAndBin(t *testing.T) {
	ctx := context.Background()
	failing, _ := fakeCLI(t, "◒ working\r│ resolving\nError: repo not found\n", 1)
	in := &SkillsInstaller{SkillsBin: failing}
	if in.Kind() != KindSkill {
		t.Fatal("kind")
	}
	err := in.Install(ctx, "x", CatalogEntry{Install: SkillInstallSpec{Repo: "o/r", Skill: "s"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "skills add o/r@s") || !strings.Contains(err.Error(), "Error: repo not found") || strings.Contains(err.Error(), "◒") {
		t.Fatalf("install err = %v", err)
	}
	if err := in.Remove(ctx, "x"); err == nil || !strings.Contains(err.Error(), "repo not found") {
		t.Fatalf("remove err = %v", err)
	}
	if err := in.Remove(ctx, " "); err == nil {
		t.Fatal("empty remove accepted")
	}
	if err := in.Install(ctx, "x", CatalogEntry{Install: "nope"}, nil); err == nil {
		t.Fatal("non-skill entry accepted")
	}
	// A bin with arguments runs through sh.
	ok, log := fakeCLI(t, "", 0)
	if err := (&SkillsInstaller{SkillsBin: ok + " --quiet"}).Remove(ctx, "y"); err != nil {
		t.Fatal(err)
	}
	if c := readLog(t, log); !strings.Contains(c, "--quiet remove y --global --yes") {
		t.Fatalf("calls = %s", c)
	}
	// With nothing on PATH, the installer says so.
	t.Setenv("PATH", t.TempDir())
	if _, _, err := (&SkillsInstaller{}).bin(); err == nil {
		t.Fatal("missing skills and npx not reported")
	}
	if got := lastLines([]byte("a\nb\nc\n"), 2); got != "b | c" {
		t.Fatalf("lastLines = %q", got)
	}
}

func TestSharedSkillsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d, err := SharedSkillsDir()
	if err != nil || d != filepath.Join(home, ".agents", "skills") {
		t.Fatalf("dir = %q, %v", d, err)
	}
}

// rawGitHub answers raw.githubusercontent.com requests from a map of
// paths, in-process.
type rawGitHub map[string]string

func (g rawGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "raw.githubusercontent.com" {
		return nil, errors.New("unexpected host " + r.URL.Host)
	}
	rec := httptest.NewRecorder()
	if body, ok := g[r.URL.Path]; ok {
		_, _ = io.WriteString(rec, body)
	} else {
		http.NotFound(rec, r)
	}
	res := rec.Result()
	res.Request = r
	return res, nil
}

func TestSkillsShInspectorReadsSkillMD(t *testing.T) {
	ctx := context.Background()
	long := strings.Repeat("x", 4100)
	gh := rawGitHub{
		"/o/r/main/skills/review/SKILL.md": "---\nname: review\ndescription: Reviews code\n---\n# Review\n",
		"/o/r/main/SKILL.md":               "no frontmatter here\n" + long,
	}
	in := &SkillsShInspector{Client: &http.Client{Transport: gh}}
	if in.Kind() != KindSkill {
		t.Fatal("kind")
	}
	got, err := in.Inspect(ctx, CatalogEntry{Install: SkillInstallSpec{Repo: "o/r", Skill: "review"}})
	if err != nil || got.Summary != "Skill review from o/r: Reviews code" || !strings.Contains(got.Content, "# Review") {
		t.Fatalf("inspect = %+v, %v", got, err)
	}
	got, err = in.Inspect(ctx, CatalogEntry{Install: SkillInstallSpec{Repo: "o/r"}})
	if err != nil || !strings.HasSuffix(got.Content, "…(truncated)") {
		t.Fatalf("root inspect = %+v, %v", got.Summary, err)
	}
	got, err = in.Inspect(ctx, CatalogEntry{Install: SkillInstallSpec{Repo: "o/missing", Skill: "x"}})
	if err != nil || !strings.Contains(got.Warning, "not found") {
		t.Fatalf("missing = %+v, %v", got, err)
	}
	if _, err := in.Inspect(ctx, CatalogEntry{Install: "nope"}); err == nil {
		t.Fatal("non-skill entry accepted")
	}
	if (&SkillsShInspector{}).client() == nil || (&SkillsShSource{}).client() == nil {
		t.Fatal("default clients")
	}
	s := &SkillsShSource{}
	if s.Kind() != KindSkill || s.Name() != "skills.sh" || s.base() != "https://skills.sh" {
		t.Fatal("skills.sh source defaults")
	}
	if humanCount(2_500_000) != "2.5M" || humanCount(42) != "42" {
		t.Fatal("humanCount")
	}
}

func TestOfficialSourceDefaults(t *testing.T) {
	s := NewOfficialMCPSource()
	if s.ID() == "" || s.Kind() != KindMCP {
		t.Fatalf("official source = %+v", s)
	}
	if (&MCPInspector{}).Kind() != KindMCP {
		t.Fatal("inspector kind")
	}
}

// mcpHTTPServer is a minimal streamable-HTTP MCP server: initialize,
// tools/list, tools/call, with one answer sent as SSE.
func mcpHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		reply := func(v map[string]any) {
			v["jsonrpc"], v["id"] = "2.0", msg.ID
			_ = json.NewEncoder(w).Encode(v)
		}
		switch msg.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "s1")
			reply(map[string]any{"result": map[string]any{"protocolVersion": "2024-11-05"}})
		case "notifications/initialized":
			if r.Header.Get("Mcp-Session-Id") != "s1" {
				t.Errorf("session id not sent back")
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "text/event-stream")
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"tools": []any{
				map[string]any{"name": "echo", "description": "Echo", "inputSchema": map[string]any{"type": "object"}},
				map[string]any{"name": " "},
				"junk",
			}}})
			_, _ = io.WriteString(w, "event: message\r\ndata: "+string(b)+"\r\n\r\n")
		case "tools/call":
			name, _ := msg.Params["name"].(string)
			switch name {
			case "echo":
				reply(map[string]any{"result": map[string]any{"content": []any{
					map[string]any{"type": "text", "text": "hello"},
					map[string]any{"type": "image", "data": "..."},
					map[string]any{"type": "text", "text": "world"},
				}}})
			case "fails":
				reply(map[string]any{"result": map[string]any{"isError": true, "content": []any{}}})
			case "teapot":
				http.Error(w, strings.Repeat("t", 300), http.StatusTeapot)
			case "silent":
				w.WriteHeader(http.StatusOK)
			case "garbled":
				_, _ = io.WriteString(w, "{not json")
			default:
				reply(map[string]any{"error": map[string]any{"code": -32602, "message": "Unknown tool"}})
			}
		default:
			reply(map[string]any{"error": "no such method"})
		}
	}))
}

func TestCallMCPToolOverHTTP(t *testing.T) {
	srv := mcpHTTPServer(t)
	defer srv.Close()
	ctx := context.Background()
	spec := MCPInstallSpec{Type: "http", URL: srv.URL}

	text, err := CallMCPTool(ctx, spec, "echo", nil, time.Second)
	if err != nil || text != "hello\nworld" {
		t.Fatalf("echo = %q, %v", text, err)
	}
	if _, err := CallMCPTool(ctx, spec, "fails", nil, time.Second); err == nil || !strings.Contains(err.Error(), "returned no message") {
		t.Fatalf("isError = %v", err)
	}
	if _, err := CallMCPTool(ctx, spec, "gone", nil, time.Second); err == nil || !strings.Contains(err.Error(), "-32602") {
		t.Fatalf("unknown tool = %v", err)
	}
	_, err = CallMCPTool(ctx, spec, "teapot", nil, time.Second)
	if err == nil || !strings.Contains(err.Error(), "http 418") || !strings.Contains(err.Error(), "…") {
		t.Fatalf("http error = %v", err)
	}
	if _, err := CallMCPTool(ctx, spec, "silent", nil, time.Second); err == nil || !strings.Contains(err.Error(), "no response") {
		t.Fatalf("empty answer = %v", err)
	}
	if _, err := CallMCPTool(ctx, spec, "garbled", nil, time.Second); err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("garbled = %v", err)
	}
	if _, err := CallMCPTool(ctx, spec, " ", nil, time.Second); err == nil {
		t.Fatal("blank tool accepted")
	}

	sess, err := openMCPSession(ctx, MCPInstallSpec{Type: "streamable-http", URL: srv.URL}, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	tools, err := listMCPTools(ctx, sess)
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" || tools[0].InputSchema != `{"type":"object"}` {
		t.Fatalf("tools = %+v, %v", tools, err)
	}
	env, err := sess.call(ctx, "resources/list", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resultOf(env); err == nil || !strings.Contains(err.Error(), "no such method") {
		t.Fatalf("plain error member = %v", err)
	}
}

func TestOpenMCPSessionRefusals(t *testing.T) {
	ctx := context.Background()
	for _, spec := range []MCPInstallSpec{
		{Type: "stdio"},
		{Type: "http"},
		{Type: "ws", URL: "ws://x"},
	} {
		if _, err := openMCPSession(ctx, spec, time.Second); err == nil {
			t.Errorf("%+v accepted", spec)
		}
	}
	// A server that refuses to initialize.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"message":"go away"}}`)
	}))
	defer srv.Close()
	if _, err := openMCPSession(ctx, MCPInstallSpec{Type: "sse", URL: srv.URL}, time.Second); err == nil || !strings.Contains(err.Error(), "rejected initialize") {
		t.Fatalf("rejected init = %v", err)
	}
	// One that is not there.
	srv2 := httptest.NewServer(http.NotFoundHandler())
	srv2.Close()
	if _, err := openMCPSession(ctx, MCPInstallSpec{Type: "http", URL: srv2.URL}, time.Second); err == nil || !strings.Contains(err.Error(), "initialize failed") {
		t.Fatalf("down server = %v", err)
	}
	if (&rpcError{Message: "m"}).Error() != "mcp: server error: m" {
		t.Fatal("rpcError without code")
	}
}
