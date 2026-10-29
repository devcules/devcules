package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// lockedBuffer is a log sink that tests can read while serve writes.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type fixture struct {
	srv    *Server
	tokens *launch.Tokens
	audit  *audit.Log
	token  string
	launch launch.Launch
	logs   *lockedBuffer
}

const publicURL = "https://devcules.example.test"

func newFixture(t *testing.T, extra ...Tool) *fixture {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	tokens := launch.New(s)
	token, l, err := tokens.Mint(ctx, "mcp test", "meta", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	echo := Tool{
		Name: "echo", Description: "Echoes its text.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
		Call: func(_ context.Context, c Caller, args json.RawMessage) (Result, error) {
			var in struct{ Text string }
			if err := DecodeArgs(args, &in); err != nil {
				return Errorf("bad arguments"), nil
			}
			return Result{Text: in.Text + " from " + c.Label}, nil
		},
	}
	if err := reg.Register(append([]Tool{echo}, extra...)...); err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	log := audit.New(s, nil)
	srv, err := New(Options{Tokens: tokens, Tools: reg, AuditLog: log, PublicURL: publicURL + "/", Version: "1.2.3",
		Instructions: "Devcules tools.", Log: slog.New(slog.NewTextHandler(logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{srv: srv, tokens: tokens, audit: log, token: token, launch: l, logs: logs}
}

// reply is one decoded answer.
type reply struct {
	code   int
	header http.Header
	raw    string
	body   struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  map[string]any  `json:"result"`
		Error   *struct {
			Code    int            `json:"code"`
			Message string         `json:"message"`
			Data    map[string]any `json:"data"`
		} `json:"error"`
	}
}

func (f *fixture) do(t *testing.T, method, body string, headers map[string]string) reply {
	t.Helper()
	r := httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+f.token)
	for k, v := range headers {
		if v == "" {
			r.Header.Del(k)
		} else {
			r.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	f.srv.ServeHTTP(w, r)
	out := reply{code: w.Code, header: w.Header(), raw: w.Body.String()}
	if out.raw != "" {
		if err := json.Unmarshal(w.Body.Bytes(), &out.body); err != nil {
			t.Fatalf("answer is not JSON: %q", out.raw)
		}
	}
	if w.Header().Get("Mcp-Session-Id") != "" {
		t.Fatal("the server must never hand out a session id")
	}
	return out
}

// modern sends one 2026-07-28 request with matching headers.
func (f *fixture) modern(t *testing.T, id any, method string, params map[string]any) reply {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = map[string]any{
		metaVersion:      Version20260728,
		metaCapabilities: map[string]any{},
		metaClientInfo:   map[string]any{"name": "test", "version": "1"},
	}
	h := map[string]string{"MCP-Protocol-Version": Version20260728, "Mcp-Method": method}
	if name, ok := params["name"].(string); ok {
		h["Mcp-Name"] = name
	}
	return f.do(t, http.MethodPost, rpc(id, method, params), h)
}

// legacy sends one initialize-era request under version (none when "").
func (f *fixture) legacy(t *testing.T, version string, id any, method string, params map[string]any) reply {
	t.Helper()
	return f.do(t, http.MethodPost, rpc(id, method, params), map[string]string{"MCP-Protocol-Version": version})
}

func rpc(id any, method string, params map[string]any) string {
	m := map[string]any{"jsonrpc": "2.0", "method": method}
	if id != nil {
		m["id"] = id
	}
	if params != nil {
		m["params"] = params
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func wantError(t *testing.T, r reply, status, code int, msg string) {
	t.Helper()
	if r.code != status || r.body.Error == nil || r.body.Error.Code != code || !strings.Contains(r.body.Error.Message, msg) {
		t.Fatalf("got %d %s, want %d with error %d %q", r.code, r.raw, status, code, msg)
	}
	if r.body.Result != nil {
		t.Fatalf("an error must carry no result: %s", r.raw)
	}
}

func TestNewNeedsTokensAndTools(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New with nothing must fail")
	}
}

func TestTransportGuards(t *testing.T) {
	f := newFixture(t)
	ping := rpc(1, "ping", nil)

	for _, m := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		r := f.do(t, m, "", nil)
		wantError(t, r, http.StatusMethodNotAllowed, codeInvalidRequest, "POST only")
		if r.header.Get("Allow") != http.MethodPost {
			t.Fatalf("%s: Allow = %q", m, r.header.Get("Allow"))
		}
	}

	wantError(t, f.do(t, http.MethodPost, ping, map[string]string{"Origin": "https://evil.example"}), http.StatusForbidden, codeInvalidRequest, "origin")
	if r := f.do(t, http.MethodPost, ping, map[string]string{"Origin": publicURL}); r.code != http.StatusOK {
		t.Fatalf("own origin = %d %s", r.code, r.raw)
	}

	r := f.do(t, http.MethodPost, ping, map[string]string{"Authorization": ""})
	wantError(t, r, http.StatusUnauthorized, codeInvalidRequest, "launch token")
	if !strings.HasPrefix(r.header.Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("WWW-Authenticate = %q", r.header.Get("WWW-Authenticate"))
	}
	wantError(t, f.do(t, http.MethodPost, ping, map[string]string{"Authorization": "Bearer dvl_nope"}), http.StatusUnauthorized, codeInvalidRequest, "invalid launch token")

	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"` + strings.Repeat("x", maxBody) + `"}}`
	wantError(t, f.do(t, http.MethodPost, big, nil), http.StatusRequestEntityTooLarge, codeInvalidRequest, "1 MiB")
	wantError(t, f.do(t, http.MethodPost, "["+ping+"]", nil), http.StatusBadRequest, codeInvalidRequest, "batches")
	wantError(t, f.do(t, http.MethodPost, "{nope", nil), http.StatusBadRequest, codeParse, "parse error")
	wantError(t, f.do(t, http.MethodPost, `{"jsonrpc":"1.0","id":1,"method":"ping"}`, nil), http.StatusBadRequest, codeInvalidRequest, `"2.0"`)
	wantError(t, f.do(t, http.MethodPost, `{"jsonrpc":"2.0","id":1,"result":{}}`, nil), http.StatusBadRequest, codeInvalidRequest, "responses")
	wantError(t, f.do(t, http.MethodPost, `{"jsonrpc":"2.0","id":1}`, nil), http.StatusBadRequest, codeInvalidRequest, "method")
	wantError(t, f.do(t, http.MethodPost, `{"jsonrpc":"2.0","id":null,"method":"ping"}`, nil), http.StatusBadRequest, codeInvalidRequest, "null")
	wantError(t, f.do(t, http.MethodPost, `{"jsonrpc":"2.0","id":1,"method":"ping","params":[1]}`, nil), http.StatusBadRequest, codeInvalidParams, "object")

	// A notification is accepted with no body, in either era.
	for _, n := range []string{rpc(nil, "notifications/initialized", nil), rpc(nil, "notifications/cancelled", map[string]any{"requestId": 1})} {
		if r := f.do(t, http.MethodPost, n, nil); r.code != http.StatusAccepted || r.raw != "" {
			t.Fatalf("notification = %d %q", r.code, r.raw)
		}
	}
}

func TestRevokedToken(t *testing.T) {
	f := newFixture(t)
	if err := f.tokens.Revoke(context.Background(), f.launch.ID); err != nil {
		t.Fatal(err)
	}
	wantError(t, f.do(t, http.MethodPost, rpc(1, "ping", nil), nil), http.StatusUnauthorized, codeInvalidRequest, "revoked")
}

func TestModernDiscover(t *testing.T) {
	f := newFixture(t)
	r := f.modern(t, "a-1", "server/discover", nil)
	if r.code != http.StatusOK || string(r.body.ID) != `"a-1"` || r.body.JSONRPC != "2.0" {
		t.Fatalf("discover = %d %s", r.code, r.raw)
	}
	res := r.body.Result
	versions, _ := res["supportedVersions"].([]any)
	if len(versions) != 4 || versions[0] != Version20260728 {
		t.Fatalf("supportedVersions = %v", versions)
	}
	if res["resultType"] != "complete" || res["instructions"] != "Devcules tools." || res["cacheScope"] != "public" || res["ttlMs"] != float64(300000) {
		t.Fatalf("discover result = %v", res)
	}
	caps, _ := res["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("capabilities = %v", caps)
	}
	meta, _ := res["_meta"].(map[string]any)
	info, _ := meta[metaServerInfo].(map[string]any)
	if info["name"] != "devcules" || info["version"] != "1.2.3" {
		t.Fatalf("serverInfo = %v", meta)
	}
	if r.header.Get("Cache-Control") != "no-store" || r.header.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", r.header)
	}
}

func TestModernChecks(t *testing.T) {
	f := newFixture(t)
	base := map[string]string{"MCP-Protocol-Version": Version20260728, "Mcp-Method": "tools/list"}
	withMeta := func(meta map[string]any) string {
		return rpc(1, "tools/list", map[string]any{"_meta": meta})
	}
	good := map[string]any{metaVersion: Version20260728, metaCapabilities: map[string]any{}}

	// Capabilities missing, or not an object.
	wantError(t, f.do(t, http.MethodPost, withMeta(map[string]any{metaVersion: Version20260728}), base), http.StatusBadRequest, codeInvalidParams, metaCapabilities)
	wantError(t, f.do(t, http.MethodPost, withMeta(map[string]any{metaVersion: Version20260728, metaCapabilities: "x"}), base), http.StatusBadRequest, codeInvalidParams, metaCapabilities)
	// The header names the modern era but _meta is missing.
	wantError(t, f.do(t, http.MethodPost, rpc(1, "tools/list", nil), base), http.StatusBadRequest, codeInvalidParams, metaVersion)

	r := f.do(t, http.MethodPost, withMeta(map[string]any{metaVersion: "2027-01-01", metaCapabilities: map[string]any{}}), base)
	wantError(t, r, http.StatusBadRequest, codeUnsupportedVersion, "Unsupported protocol version")
	if r.body.Error.Data["requested"] != "2027-01-01" || len(r.body.Error.Data["supported"].([]any)) != 4 {
		t.Fatalf("unsupported data = %v", r.body.Error.Data)
	}

	for name, h := range map[string]map[string]string{
		"version header": {"MCP-Protocol-Version": Version20251125, "Mcp-Method": "tools/list"},
		"no version":     {"MCP-Protocol-Version": "", "Mcp-Method": "tools/list"},
		"method header":  {"MCP-Protocol-Version": Version20260728, "Mcp-Method": "tools/call"},
		"no method":      {"MCP-Protocol-Version": Version20260728},
	} {
		t.Run(name, func(t *testing.T) {
			wantError(t, f.do(t, http.MethodPost, withMeta(good), h), http.StatusBadRequest, codeHeaderMismatch, "header")
		})
	}
	call := rpc(1, "tools/call", map[string]any{"name": "echo", "_meta": good})
	wantError(t, f.do(t, http.MethodPost, call, map[string]string{"MCP-Protocol-Version": Version20260728, "Mcp-Method": "tools/call", "Mcp-Name": "status"}),
		http.StatusBadRequest, codeHeaderMismatch, "Mcp-Name")

	// Methods the modern era dropped, and ones that never existed.
	for _, m := range []string{"initialize", "ping", "resources/list", "nope"} {
		wantError(t, f.modern(t, 1, m, nil), http.StatusNotFound, codeMethodNotFound, m)
	}
}

func TestModernTools(t *testing.T) {
	more := []Tool{}
	for _, n := range []string{"b", "a", "c"} {
		more = append(more, Tool{Name: "t." + n, Description: n, InputSchema: noArgs,
			Call: func(context.Context, Caller, json.RawMessage) (Result, error) { return Result{Text: n}, nil }})
	}
	f := newFixture(t, more...)
	f.srv.o.PageSize = 2

	var names []string
	var cursor any
	for page := 0; ; page++ {
		params := map[string]any{}
		if cursor != nil {
			params["cursor"] = cursor
		}
		r := f.modern(t, page, "tools/list", params)
		if r.code != 200 || r.body.Result["ttlMs"] != float64(300000) || r.body.Result["cacheScope"] != "public" || r.body.Result["resultType"] != "complete" {
			t.Fatalf("list = %d %s", r.code, r.raw)
		}
		tools, _ := r.body.Result["tools"].([]any)
		for _, x := range tools {
			tm := x.(map[string]any)
			if _, ok := tm["inputSchema"].(map[string]any); !ok {
				t.Fatalf("tool without inputSchema: %v", tm)
			}
			names = append(names, tm["name"].(string))
		}
		next, ok := r.body.Result["nextCursor"]
		if !ok {
			break
		}
		cursor = next
		if page > 5 {
			t.Fatal("pagination does not end")
		}
	}
	if strings.Join(names, ",") != "echo,t.a,t.b,t.c" {
		t.Fatalf("tools = %v", names)
	}
	for _, bad := range []any{"!!", "bzpm", 7, "bzotOTk"} { // garbage, "o:-", a number, "o:99"
		wantError(t, f.modern(t, 1, "tools/list", map[string]any{"cursor": bad}), http.StatusOK, codeInvalidParams, "invalid cursor")
	}

	r := f.modern(t, 9, "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"text": "hi"}})
	content, _ := r.body.Result["content"].([]any)
	if r.code != 200 || len(content) != 1 || content[0].(map[string]any)["text"] != "hi from mcp test" || r.body.Result["isError"] != false || r.body.Result["resultType"] != "complete" {
		t.Fatalf("call = %d %s", r.code, r.raw)
	}
	wantError(t, f.modern(t, 1, "tools/call", map[string]any{"name": "nope"}), http.StatusOK, codeInvalidParams, "Unknown tool: nope")
	wantError(t, f.modern(t, 1, "tools/call", map[string]any{}), http.StatusOK, codeInvalidParams, "params.name")
}

func TestLegacyFlow(t *testing.T) {
	f := newFixture(t)
	for requested, want := range map[string]string{
		Version20251125: Version20251125,
		Version20250618: Version20250618,
		Version20250326: Version20250326,
		"2024-11-05":    Version20251125,
		"":              Version20251125,
	} {
		r := f.legacy(t, "", 1, "initialize", map[string]any{"protocolVersion": requested, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "old", "version": "0"}})
		info, _ := r.body.Result["serverInfo"].(map[string]any)
		if r.code != 200 || r.body.Result["protocolVersion"] != want || info["name"] != "devcules" || r.body.Result["instructions"] != "Devcules tools." {
			t.Fatalf("initialize %q = %d %s", requested, r.code, r.raw)
		}
		if _, ok := r.body.Result["resultType"]; ok {
			t.Fatal("legacy results carry no resultType")
		}
	}
	if r := f.legacy(t, Version20250618, 2, "ping", nil); r.code != 200 || r.body.Result == nil || len(r.body.Result) != 0 {
		t.Fatalf("ping = %d %s", r.code, r.raw)
	}
	// No header at all means 2025-03-26, which is served.
	if r := f.legacy(t, "", 3, "tools/list", nil); r.code != 200 || len(r.body.Result["tools"].([]any)) != 1 {
		t.Fatalf("list without header = %d %s", r.code, r.raw)
	}
	r := f.legacy(t, "1999-01-01", 4, "tools/list", nil)
	wantError(t, r, http.StatusBadRequest, codeInvalidRequest, "MCP-Protocol-Version")
	if r.body.Error.Data["requested"] != "1999-01-01" {
		t.Fatalf("data = %v", r.body.Error.Data)
	}
	wantError(t, f.legacy(t, Version20251125, 5, "tools/list", map[string]any{"cursor": "!!"}), http.StatusOK, codeInvalidParams, "invalid cursor")
	if r := f.legacy(t, Version20251125, 6, "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"text": "yo"}}); r.code != 200 || r.body.Result["isError"] != false {
		t.Fatalf("call = %d %s", r.code, r.raw)
	}
	wantError(t, f.legacy(t, Version20251125, 7, "tools/call", map[string]any{"name": "nope"}), http.StatusOK, codeInvalidParams, "Unknown tool")
	// A legacy client gets JSON-RPC's method-not-found inside a 200.
	wantError(t, f.legacy(t, Version20251125, 8, "resources/list", nil), http.StatusOK, codeMethodNotFound, "resources/list")
	if r := f.legacy(t, Version20251125, 9, "server/discover", nil); r.body.Error == nil {
		t.Fatal("server/discover is modern only")
	}
}

func TestCallAuditLogsAndErrors(t *testing.T) {
	boom := Tool{Name: "boom", Description: "Fails.", InputSchema: noArgs,
		Call: func(context.Context, Caller, json.RawMessage) (Result, error) {
			return Result{}, errors.New("disk on fire at /secret/path")
		}}
	shaped := Tool{Name: "shaped", Description: "Structured.", InputSchema: noArgs,
		Call: func(context.Context, Caller, json.RawMessage) (Result, error) {
			return Result{Structured: map[string]int{"n": 1}}, nil
		}}
	f := newFixture(t, boom, shaped)

	r := f.modern(t, 1, "tools/call", map[string]any{"name": "boom"})
	text := r.body.Result["content"].([]any)[0].(map[string]any)["text"]
	if r.code != 200 || r.body.Result["isError"] != true || text != "internal error in boom" {
		t.Fatalf("boom = %d %s", r.code, r.raw)
	}
	r = f.modern(t, 2, "tools/call", map[string]any{"name": "shaped"})
	text = r.body.Result["content"].([]any)[0].(map[string]any)["text"]
	if sc, _ := r.body.Result["structuredContent"].(map[string]any); sc["n"] != float64(1) || text != `{"n":1}` {
		t.Fatalf("shaped = %s", r.raw)
	}
	f.modern(t, 3, "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"text": "pa55word-in-args"}})

	entries, err := f.audit.Recent(context.Background(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("audit = %+v", entries)
	}
	// Latest first: echo, shaped, boom.
	if e := entries[2]; e.Action != audit.MCPCall || e.Actor != "launch:"+f.launch.ID || e.Target != "boom" || e.Detail["error"] != "true" || e.Detail["label"] != "mcp test" {
		t.Fatalf("boom entry = %+v", e)
	}
	if e := entries[0]; e.Target != "echo" || e.Detail["error"] != "" {
		t.Fatalf("echo entry = %+v", e)
	}

	logs := f.logs.String()
	if !strings.Contains(logs, "mcp tool failed") || !strings.Contains(logs, "disk on fire") || !strings.Contains(logs, "tool=echo") {
		t.Fatalf("logs = %s", logs)
	}
	if strings.Contains(logs, "pa55word") || strings.Contains(logs, f.token) {
		t.Fatalf("logs leak arguments or the token: %s", logs)
	}
	if strings.Contains(r.raw, "disk on fire") {
		t.Fatal("the model must not see an internal error's text")
	}
}

func TestRateLimit(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	f.srv.now = func() time.Time { return now }
	call := func() bool {
		r := f.legacy(t, Version20251125, 1, "tools/call", map[string]any{"name": "echo"})
		return r.body.Result["isError"] == true
	}
	for i := range callBurst {
		if call() {
			t.Fatalf("call %d limited inside the burst", i)
		}
	}
	if !call() {
		t.Fatal("the call after the burst must be limited")
	}
	now = now.Add(time.Second)
	for i := range int(callRate) {
		if call() {
			t.Fatalf("refilled call %d limited", i)
		}
	}
	if !call() {
		t.Fatal("a second refills only callRate calls")
	}
	// Limited calls are audited as failures too.
	entries, _ := f.audit.Recent(context.Background(), 0, 1)
	if len(entries) != 1 || entries[0].Detail["error"] != "true" {
		t.Fatalf("last entry = %+v", entries)
	}
}

func TestRegistry(t *testing.T) {
	ok := Tool{Name: "ok", Description: "d", InputSchema: noArgs,
		Call: func(context.Context, Caller, json.RawMessage) (Result, error) { return Result{}, nil }}
	bad := func(mut func(*Tool)) Tool { t := ok; mut(&t); return t }
	cases := map[string]Tool{
		"empty name":    bad(func(t *Tool) { t.Name = "" }),
		"space in name": bad(func(t *Tool) { t.Name = "a b" }),
		"long name":     bad(func(t *Tool) { t.Name = strings.Repeat("a", 129) }),
		"no handler":    bad(func(t *Tool) { t.Call = nil }),
		"no description": bad(func(t *Tool) {
			t.Description = ""
		}),
		"array schema":  bad(func(t *Tool) { t.InputSchema = json.RawMessage(`[]`) }),
		"no schema":     bad(func(t *Tool) { t.InputSchema = nil }),
		"output schema": bad(func(t *Tool) { t.OutputSchema = json.RawMessage(`"x"`) }),
	}
	for name, tool := range cases {
		if err := NewRegistry().Register(tool); err == nil {
			t.Fatalf("%s: registered", name)
		}
	}
	r := NewRegistry()
	if err := r.Register(ok, bad(func(t *Tool) { t.Name = "a.b-c_D9"; t.OutputSchema = noArgs })); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(ok); err == nil {
		t.Fatal("duplicate registered")
	}
}

func TestDecodeArgs(t *testing.T) {
	var v struct{ A int }
	for _, in := range []string{"", "null", " {} ", `{"A":1}`} {
		if err := DecodeArgs(json.RawMessage(in), &v); err != nil {
			t.Fatalf("%q: %v", in, err)
		}
	}
	for _, in := range []string{`{"B":1}`, `{"A":"x"}`, `{} {}`, `[`} {
		if err := DecodeArgs(json.RawMessage(in), &v); err == nil {
			t.Fatalf("%q decoded", in)
		}
	}
}
