package api

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
	"sync"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/assistants"
	"github.com/devcules/devcules/internal/audit"
	"github.com/devcules/devcules/internal/auth"
	"github.com/devcules/devcules/internal/link/hub"
	"github.com/devcules/devcules/internal/link/linktest"
	"github.com/devcules/devcules/internal/machines"
	machrelay "github.com/devcules/devcules/internal/machines/relay"
	"github.com/devcules/devcules/internal/registry"
	"github.com/devcules/devcules/internal/store/sqlite"
)

// b2SlowAssistant takes d to answer and honours its context, as the
// real loop does (a model call or an approval wait ends when the
// request context does).
type b2SlowAssistant struct {
	echoAssistant
	d time.Duration
}

func (a b2SlowAssistant) Chat(ctx context.Context, sess, _ string) (string, []assistants.Event, error) {
	select {
	case <-time.After(a.d):
	case <-ctx.Done():
		return "", nil, errors.New("turn cancelled: " + ctx.Err().Error())
	}
	if sess == "" {
		sess = "0123456789abcdef"
	}
	return sess, []assistants.Event{{Type: assistants.EventMessage, Text: "done"}}, nil
}

// b2Server serves Options (store, auth and audit filled in) on a real
// http.Server with serve's three timeouts set to timeout, and returns
// its URL, a session cookie and the audit log.
func b2Server(t *testing.T, timeout time.Duration, o Options) (string, string, *audit.Log) {
	t.Helper()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, _, err := svc.EnsureAdmin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	log := audit.New(s, nil)
	o.Store, o.Auth, o.Web, o.AuditLog = s, svc, testWeb(), log
	mux := Handler(o)
	cookie := sessionCookie(t, mux, password)
	srv := httptest.NewUnstartedServer(mux)
	// cmd/devcules/serve.go: ReadHeaderTimeout 5s, ReadTimeout and
	// WriteTimeout 10s; scaled here when timeout is shorter.
	srv.Config.ReadHeaderTimeout = min(5*time.Second, timeout)
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = timeout, timeout
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL, cookie, log
}

func b2Do(t *testing.T, method, url, cookie, body string) (int, string, error) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, url, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), nil
}

func b2Chat(t *testing.T, timeout, turn time.Duration) {
	t.Helper()
	reg, _ := assistants.NewRegistry(b2SlowAssistant{d: turn})
	url, cookie, _ := b2Server(t, timeout, Options{Assistants: reg})
	code, body, err := b2Do(t, http.MethodPost, url+"/api/assistants/echo/chat", cookie, `{"message":"hi"}`)
	if err != nil {
		t.Fatalf("a %s chat turn on a %s server: client got %v (reply lost to the WriteTimeout)", turn, timeout, err)
	}
	if code != http.StatusOK || !strings.Contains(body, "done") || !strings.Contains(body, `"session":"0123456789abcdef"`) {
		t.Fatalf("chat reply lost: %d %q", code, body)
	}
}

// TestChatOutlivesServerTimeouts: a chat turn longer than the server's
// read and write timeouts (an approval wait, a slow model) still gets
// its reply (GOSEC-01). Scaled to a 1 s server so it runs in every test pass.
func TestChatOutlivesServerTimeouts(t *testing.T) {
	t.Parallel()
	b2Chat(t, time.Second, 1500*time.Millisecond)
}

// TestChatOutlivesWriteTimeout is the finder's proof at serve's real
// 10 s timeouts with an 11 s turn.
func TestChatOutlivesWriteTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("11 s turn; TestChatOutlivesServerTimeouts covers this at scale")
	}
	t.Parallel()
	b2Chat(t, 10*time.Second, 11*time.Second)
}

// TestDeskDataOutlivesServerTimeouts: a desk fetch past the server's
// timeouts still answers (the upstream clients time out at 12 s and
// 30 s, past serve's 10 s).
func TestDeskDataOutlivesServerTimeouts(t *testing.T) {
	t.Parallel()
	slow := func(ctx context.Context, _ bool) (json.RawMessage, error) {
		select {
		case <-time.After(1500 * time.Millisecond):
			return json.RawMessage(`{"configured":true}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	srv := httptest.NewUnstartedServer(deskData(slow))
	srv.Config.ReadHeaderTimeout, srv.Config.ReadTimeout, srv.Config.WriteTimeout = time.Second, time.Second, time.Second
	srv.Start()
	defer srv.Close()
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("desk data reply lost: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(b), "configured") {
		t.Fatalf("desk data = %d %q", res.StatusCode, b)
	}
}

// b2FakeClaude writes a stand-in `claude` CLI that sleeps, logs its
// arguments and succeeds.
func b2FakeClaude(t *testing.T, sleep string) (bin, log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls.log")
	bin = filepath.Join(dir, "claude")
	script := "#!/bin/sh\nsleep " + sleep + "\nprintf '%s\\n' \"$*\" >> '" + log + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, log
}

func b2AuditRows(t *testing.T, log *audit.Log, action string) []audit.Entry {
	t.Helper()
	all, err := log.Recent(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []audit.Entry
	for _, e := range all {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

// TestRegistryInstallAuditedAndOutlivesTimeouts: an MCP install that
// takes longer than the server's timeouts still answers ok, and an
// install and a remove each file one audit row that names what will
// run but none of the env values (GOSEC-01, GOSEC-13).
func TestRegistryInstallAuditedAndOutlivesTimeouts(t *testing.T) {
	t.Parallel()
	bin, calls := b2FakeClaude(t, "1.5")
	url, cookie, log := b2Server(t, time.Second, Options{ClaudeBin: bin})
	install := `{"name":"weather","entry":{"id":"mcp-official:weather","install":{"type":"stdio","command":"npx","args":["-y","weather-mcp","--token","argsecret123"]}},"params":{"API_KEY":"envsecret456"}}`
	code, body, err := b2Do(t, http.MethodPost, url+"/api/registry/mcp/install", cookie, install)
	if err != nil || code != http.StatusOK {
		t.Fatalf("install = %d %q %v", code, body, err)
	}
	if b, _ := os.ReadFile(calls); !strings.Contains(string(b), "mcp add-json weather") {
		t.Fatalf("fake claude not called: %q", b)
	}
	rows := b2AuditRows(t, log, audit.RegistryInstall)
	if len(rows) != 1 {
		t.Fatalf("registry.install rows = %d, want 1", len(rows))
	}
	e := rows[0]
	if e.Target != "weather" || e.Detail["kind"] != "mcp" || e.Detail["command"] != "npx" || e.Detail["args"] != "4" || e.Actor != auth.AdminUsername {
		t.Fatalf("install row = %+v", e)
	}
	raw, _ := json.Marshal(e)
	if strings.Contains(string(raw), "envsecret456") || strings.Contains(string(raw), "argsecret123") {
		t.Fatalf("install row leaks a secret: %s", raw)
	}

	code, body, err = b2Do(t, http.MethodDelete, url+"/api/registry/mcp/installed/weather", cookie, "")
	if err != nil || code != http.StatusOK {
		t.Fatalf("remove = %d %q %v", code, body, err)
	}
	rows = b2AuditRows(t, log, audit.RegistryRemove)
	if len(rows) != 1 || rows[0].Target != "weather" || rows[0].Detail["kind"] != "mcp" {
		t.Fatalf("registry.remove rows = %+v, want one for weather", rows)
	}
}

// TestRegistryFailedInstallNotAudited: an install the CLI refuses
// changed nothing, so it files no row.
func TestRegistryFailedInstallNotAudited(t *testing.T) {
	t.Parallel()
	url, cookie, log := b2Server(t, 10*time.Second, Options{ClaudeBin: "/bin/false"})
	install := `{"name":"weather","entry":{"install":{"type":"http","url":"https://u:p@mcp.example/x?token=s"}}}`
	if code, body, err := b2Do(t, http.MethodPost, url+"/api/registry/mcp/install", cookie, install); err != nil || code != http.StatusInternalServerError {
		t.Fatalf("install = %d %q %v", code, body, err)
	}
	if rows := b2AuditRows(t, log, audit.RegistryInstall); len(rows) != 0 {
		t.Fatalf("a failed install was audited: %+v", rows)
	}
}

// installCommandCases is every command form the checkers raised
// (rounds 1-3), with what the install row records. Anything shell-like
// before the program records "(unparsed)": safe beats informative.
var installCommandCases = []struct{ command, want string }{
	// plain forms
	{"   ", ""},
	{"npx", "npx"},
	{"/usr/local/bin/uvx", "uvx"},
	{"GITHUB_TOKEN=ghp_X npx", "npx"},
	{"A=1 B=sk-secret /opt/tok-dir/node", "node"},
	{"X= npx", "npx"},
	{"A=1\tB=ghp_X\tnpx -y", "npx"},
	{"env X=1 npx", "env"},
	{"env -i TOKEN=sk-live-X npx", "env"},
	{"sudo -E TOKEN=sk-live-X npx", "sudo"},
	{"-- npx", "--"},
	// Windows paths (claude/codex take the command literally)
	{`C:\tools\uvx.exe`, "uvx.exe"},
	{"C:/tools/uvx.exe", "uvx.exe"},
	{`GITHUB_TOKEN=ghp_X C:\tools\uvx.exe`, "uvx.exe"},
	{`"C:\Program Files\nodejs\npx.cmd"`, "(unparsed)"},
	// round-3 leaks: command substitution and ANSI-C quoting
	{"KEY=$(printf sk-live-X) npx", "(unparsed)"},
	{"KEY=`echo sk-live-X` npx", "(unparsed)"},
	{"$'sk-live X' npx", "(unparsed)"},
	{"GITHUB_TOKEN=$(gh auth token) npx", "(unparsed)"},
	{"KEY=${SECRET:-sk-live-X} npx", "(unparsed)"},
	{`KEY="${A} sk-live-X" npx`, "(unparsed)"},
	// quoting and escapes (round 2-3 forms)
	{`API_KEY="Bearer sk-live-X" npx`, "(unparsed)"},
	{`API_KEY='Bearer sk-live-X' AUTH=a\ b /usr/bin/npx`, "(unparsed)"},
	{`API_KEY="Bearer sk-live-X npx`, "(unparsed)"},
	{`"/opt/my tools/uvx"`, "(unparsed)"},
	{`A=a\ sk-live-X npx`, "(unparsed)"},
	{`A="a\"b sk-live-X" npx`, "(unparsed)"},
	{`A='a\' npx`, "(unparsed)"},
	{`A='x'"y sk-live-X" npx`, "(unparsed)"},
	{"A=b\\\nsk-live-X npx", "(unparsed)"},
	{"A=1\nsk-live-X npx", "(unparsed)"},
	{`""`, "(unparsed)"},
	{`'' npx`, "(unparsed)"},
	{`X="" npx`, "(unparsed)"},
	{`\`, "(unparsed)"},
	{`A=b\`, "(unparsed)"},
	{`"unterminated`, "(unparsed)"},
	{`'unterm`, "(unparsed)"},
	// separators and redirections
	{"TOKEN=x; sk-live-X", "(unparsed)"},
	{"TOKEN=x|sk-live-X", "(unparsed)"},
	{"TOKEN=x&&sk-live-X", "(unparsed)"},
	{"TOKEN=x <sk-live-X npx", "(unparsed)"},
	// fields that are not plain identifiers or plain program names
	{"X-Y=sk-live-X npx", "(unparsed)"},
	{"Ünïcödé=ключ npx", "(unparsed)"},
	{"/opt/ツール/uvx", "(unparsed)"},
	{"npx\u00a0sk-live-X", "(unparsed)"},
	{"A=1\u00a0sk-live-X npx", "npx"},
	{"#sk-live-X npx", "(unparsed)"},
	{"/usr/bin/", "(unparsed)"},
}

// TestInstallDetailDropsURLSecrets: the install row keeps what will run
// but no credential, wherever the endpoint or repo carries it (the
// path of a hosted MCP URL, userinfo, query, fragment, an env
// assignment in front of the command).
func TestInstallDetailDropsURLSecrets(t *testing.T) {
	mcp := func(spec registry.MCPInstallSpec) map[string]string {
		return installDetail(registry.KindMCP, registry.CatalogEntry{Install: spec})
	}
	for _, c := range []struct{ url, want string }{
		{"https://u:p@mcp.example/x?token=s#frag", "https://mcp.example"},
		{"https://actions.zapier.com/mcp/sk-ak-SECRETKEY123/sse", "https://actions.zapier.com"},
		{"https://mcp.zapier.com/api/mcp/s/SECRETTOKEN456/mcp", "https://mcp.zapier.com"},
		{"http://localhost:8931/mcp", "http://localhost:8931"},
		{"not a url", "(unparsed)"},
	} {
		d := mcp(registry.MCPInstallSpec{Type: "sse", URL: c.url})
		if d["url"] != c.want || d["type"] != "sse" {
			t.Errorf("url %q: detail = %v, want url %q", c.url, d, c.want)
		}
	}
	for _, c := range installCommandCases {
		d := mcp(registry.MCPInstallSpec{Type: "stdio", Command: c.command, Args: []string{"-y", "--key", "argsecret"}})
		if d["command"] != c.want || d["args"] != "3" {
			t.Errorf("command %q: detail = %v, want command %q", c.command, d, c.want)
		}
	}
	for _, c := range []struct{ repo, want string }{
		{"o/r", "o/r"},
		{"o/r@skill", "o/r@skill"},
		{"https://user:ghp_TOKEN789@github.com/o/r", "https://github.com/o/r"},
		{"https://github.com/o/r?access_token=ghp_Q#ghp_F", "https://github.com/o/r"},
		{"user:ghp_TOKEN789@github.com:o/r", "github.com:o/r"},
		{"//user:ghp_TOKEN789@github.com/o/r?t=ghp_Q", "//github.com/o/r"},
	} {
		d := installDetail(registry.KindSkill, registry.CatalogEntry{Install: registry.SkillInstallSpec{Repo: c.repo, Skill: "s"}})
		if d["repo"] != c.want || d["skill"] != "s" || d["kind"] != "skill" {
			t.Errorf("repo %q: detail = %v, want repo %q", c.repo, d, c.want)
		}
	}
	sweep := []map[string]string{
		mcp(registry.MCPInstallSpec{Type: "sse", URL: "https://actions.zapier.com/mcp/sk-ak-SECRETKEY123/sse"}),
		installDetail(registry.KindSkill, registry.CatalogEntry{Install: registry.SkillInstallSpec{Repo: "//user:ghp_TOKEN789@github.com/o/r"}}),
		installDetail(registry.KindSkill, registry.CatalogEntry{Install: registry.SkillInstallSpec{Repo: "https://user:ghp_TOKEN789@github.com/o/r"}}),
	}
	for _, c := range installCommandCases {
		sweep = append(sweep, mcp(registry.MCPInstallSpec{Type: "stdio", Command: c.command, Args: []string{"--key", "sk-live-ARG"}}))
	}
	for _, d := range sweep {
		raw, _ := json.Marshal(d)
		for _, secret := range []string{"SECRETKEY", "ghp_", "user:", "sk-live", "sk-secret", "ключ"} {
			if strings.Contains(string(raw), secret) {
				t.Errorf("detail leaks %q: %s", secret, raw)
			}
		}
	}
}

// b2DeadlineRecorder is a ResponseWriter that records the write
// deadlines set on it (http.NewResponseController finds the method).
type b2DeadlineRecorder struct {
	*httptest.ResponseRecorder
	mu       sync.Mutex
	deadline time.Time
	wroteAt  time.Time // deadline in force when the body was first written
	sets     int
}

func (r *b2DeadlineRecorder) SetWriteDeadline(t time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deadline, r.sets = t, r.sets+1
	return nil
}

func (r *b2DeadlineRecorder) current() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deadline
}

func (r *b2DeadlineRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	if r.wroteAt.IsZero() {
		r.wroteAt = r.deadline
	}
	r.mu.Unlock()
	return r.ResponseRecorder.Write(b)
}

// b2DeadlineAssistant notes the write deadline in force while its turn
// runs.
type b2DeadlineAssistant struct {
	echoAssistant
	rec   *b2DeadlineRecorder
	seen  chan time.Time // deadline in force during the turn
	ended chan time.Time // when the turn ended
}

func (a b2DeadlineAssistant) Chat(context.Context, string, string) (string, []assistants.Event, error) {
	a.seen <- a.rec.current()
	time.Sleep(50 * time.Millisecond)
	a.ended <- time.Now()
	return "0123456789abcdef", []assistants.Event{{Type: assistants.EventMessage, Text: "done"}}, nil
}

// b2CheckArmed: no write deadline while the work ran, and the answer
// went out under a deadline set after the work, answerWindow ahead.
func b2CheckArmed(t *testing.T, rec *b2DeadlineRecorder, during time.Time, workEnd time.Time) {
	t.Helper()
	if !during.IsZero() {
		t.Fatalf("the work ran under a write deadline (%s from its start): a longer turn loses its reply", time.Until(during).Round(time.Second))
	}
	rec.mu.Lock()
	wrote := rec.wroteAt
	rec.mu.Unlock()
	if wrote.IsZero() {
		t.Fatal("the answer was written with no write deadline (slow-reader protection lost)")
	}
	if wrote.Before(workEnd.Add(answerWindow)) || wrote.After(time.Now().Add(answerWindow)) {
		t.Fatalf("answer deadline %s, want answerWindow after the work ended (%s)", wrote, workEnd.Add(answerWindow))
	}
}

// TestChatTurnLongerThanOldWindow: round 1 bounded a turn by one
// approval wait + 5 min of model calls (15 min 10 s), less than
// MaxTurns x CallTimeout or several approvals allow. Now no write
// deadline is in force during the turn, whatever its length, and the
// answer is written under a fresh now + answerWindow deadline set after
// the turn (GOSEC-01). The deadlines are observed directly, as a turn
// of 15+ minutes cannot be run in a test.
func TestChatTurnLongerThanOldWindow(t *testing.T) {
	t.Parallel()
	rec := &b2DeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	a := b2DeadlineAssistant{rec: rec, seen: make(chan time.Time, 1), ended: make(chan time.Time, 1)}
	reg, _ := assistants.NewRegistry(a)
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, _, err := svc.EnsureAdmin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Assistants: reg})
	cookie := sessionCookie(t, mux, password)
	// serve's deadline as the connection arms it when the request is read.
	_ = rec.SetWriteDeadline(time.Now().Add(10 * time.Second))
	req := httptest.NewRequest(http.MethodPost, "/api/assistants/echo/chat", strings.NewReader(`{"message":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "done") {
		t.Fatalf("chat = %d %q", rec.Code, rec.Body.String())
	}
	b2CheckArmed(t, rec, <-a.seen, <-a.ended)
}

// TestDeskDataNoWorkWindow: the same for a desk fetch (the ICS feeds
// are fetched one after another, with no cap on their number).
func TestDeskDataNoWorkWindow(t *testing.T) {
	t.Parallel()
	rec := &b2DeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	_ = rec.SetWriteDeadline(time.Now().Add(10 * time.Second))
	var during, ended time.Time
	fetch := func(context.Context, bool) (json.RawMessage, error) {
		during = rec.current()
		time.Sleep(50 * time.Millisecond)
		ended = time.Now()
		return json.RawMessage(`{"configured":true}`), nil
	}
	deskData(fetch)(rec, httptest.NewRequest(http.MethodGet, "/api/desk/meetings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("desk data = %d %q", rec.Code, rec.Body.String())
	}
	b2CheckArmed(t, rec, during, ended)
}

// TestMachineInputAudited: each take-control input files one
// machine.input row naming the machine and the event kind, and the
// typed text and key never reach the log (GOSEC-13).
func TestMachineInputAudited(t *testing.T) {
	t.Parallel()
	s, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	svc := auth.New(s)
	password, _, err := svc.EnsureAdmin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h := hub.New(hub.HashToken(machviewTestToken), hub.Options{
		HelloTimeout: 5 * time.Second, PingInterval: time.Second, PingTimeout: 5 * time.Second, Machines: testMachineKeys,
	})
	r := machrelay.New(h)
	r.Run()
	log := audit.New(s, nil)
	mux := Handler(Options{Store: s, Auth: svc, Web: testWeb(), Link: h, Enrol: machines.NewIssuer(s), Machines: r, AuditLog: log})
	pipe := linktest.Serve(t, mux)
	linkFakeMachine(t, pipe, "b2-mac")
	for deadline := time.Now().Add(10 * time.Second); len(h.Machines()) != 1 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	session := sessionCookie(t, mux, password)
	for _, body := range []string{`{"kind":"text","text":"hunter2-typed-password"}`, `{"kind":"key","key":"secretkeyname"}`} {
		if w := machviewRequest(t, mux, http.MethodPost, "/api/machines-input", session, body); w.Code != http.StatusNoContent {
			t.Fatalf("input %s = %d (body %q)", body, w.Code, w.Body.String())
		}
	}
	rows := b2AuditRows(t, log, audit.MachineInput)
	if len(rows) != 2 {
		t.Fatalf("machine.input rows = %d, want 2", len(rows))
	}
	kinds := map[string]bool{}
	for _, e := range rows {
		if e.Target != "b2-mac" || e.Actor != auth.AdminUsername {
			t.Fatalf("input row = %+v", e)
		}
		kinds[e.Detail["kind"]] = true
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "secretkeyname") {
			t.Fatalf("input row leaks what was typed: %s", raw)
		}
	}
	if !kinds["text"] || !kinds["key"] {
		t.Fatalf("kinds = %v", kinds)
	}
	// A refused input (bad body) files nothing.
	if w := machviewRequest(t, mux, http.MethodPost, "/api/machines-input", session, `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad input = %d", w.Code)
	}
	if n := len(b2AuditRows(t, log, audit.MachineInput)); n != 2 {
		t.Fatalf("rows after a refused input = %d, want 2", n)
	}
}

// b2SlowSource answers its search after d, with an error when fail is
// set (an unreachable or slow upstream registry).
type b2SlowSource struct {
	d    time.Duration
	fail bool
}

func (s b2SlowSource) ID() string                  { return "slow" }
func (s b2SlowSource) Kind() registry.ArtifactKind { return registry.KindSkill }
func (s b2SlowSource) List(ctx context.Context, _ string) ([]registry.CatalogEntry, error) {
	select {
	case <-time.After(s.d):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.fail {
		return nil, errors.New("upstream timed out")
	}
	return []registry.CatalogEntry{{ID: "slow:one", Kind: registry.KindSkill, Name: "one"}}, nil
}

// TestRegistrySearchOutlivesServerTimeouts: a registry search whose
// upstream takes longer than the server's write timeout still delivers
// its answer, the results or the 502 registry_unreachable, instead of
// an EOF (GOSEC-01 on the search route; b2-check-2 item 1).
func TestRegistrySearchOutlivesServerTimeouts(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		fail bool
		code int
		want string
	}{
		{"502", true, http.StatusBadGateway, "registry_unreachable"},
		{"200", false, http.StatusOK, `"slow:one"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			bag := newRegistryBag("", "")
			bag.sources[registry.KindSkill] = []registry.Source{b2SlowSource{d: 1500 * time.Millisecond, fail: c.fail}}
			srv := httptest.NewUnstartedServer(registryDispatch(bag, auditor{}))
			srv.Config.ReadHeaderTimeout, srv.Config.ReadTimeout, srv.Config.WriteTimeout = time.Second, time.Second, time.Second
			srv.Start()
			t.Cleanup(srv.Close)
			start := time.Now()
			res, err := http.Get(srv.URL + "/api/registry/skill/search?q=x")
			if err != nil {
				t.Fatalf("search on a 1s server with a 1.5s source: %v (answer lost to the WriteTimeout)", err)
			}
			defer func() { _ = res.Body.Close() }()
			b, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("reading the answer: %v", err)
			}
			if el := time.Since(start); el < time.Second {
				t.Fatalf("answer after %s: the source did not outlast the write timeout", el)
			}
			if res.StatusCode != c.code || !strings.Contains(string(b), c.want) {
				t.Fatalf("search = %d %q, want %d with %s", res.StatusCode, b, c.code, c.want)
			}
		})
	}
}

// TestArmedWriterFlushes: armedWriter passes a plain http.Flusher
// assertion through, and a flush arms the answer deadline (it writes
// the header).
func TestArmedWriterFlushes(t *testing.T) {
	rec := &b2DeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := liftWrite(rec)
	f, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("liftWrite's writer is not an http.Flusher")
	}
	f.Flush()
	if !rec.Flushed {
		t.Fatal("Flush did not reach the underlying writer")
	}
	if d := rec.current(); d.IsZero() || time.Until(d) < answerWindow-time.Second {
		t.Fatalf("deadline after Flush = %v, want about now + %s", d, answerWindow)
	}
}
