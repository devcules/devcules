package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devcules/devcules/internal/launch"
	"github.com/devcules/devcules/internal/redact"
	"github.com/devcules/devcules/internal/store"
	"github.com/devcules/devcules/internal/store/sqlite"
	"github.com/devcules/devcules/internal/terminals"
	"github.com/devcules/devcules/internal/trace"
)

const fakeAWS = "AKIA" + "Z7QWERTY4UIOP3XZ"

type builtinFixture struct {
	tools    map[string]Tool
	caller   Caller
	settings *redact.Settings
}

func newBuiltins(t *testing.T, termErr error) *builtinFixture {
	t.Helper()
	ctx := context.Background()
	s, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	tokens := launch.New(s)
	_, mine, _ := tokens.Mint(ctx, "mine", "meta", time.Hour)
	_, other, _ := tokens.Mint(ctx, "other", "", time.Hour)
	traces := trace.New(s, nil)
	for i := range 3 {
		traces.Trace(ctx, store.TraceCall{LaunchID: mine.ID, At: time.Now(), Provider: "meta", Model: "muse-spark-1.3", Status: 200, Question: "q" + string(rune('0'+i))})
	}
	traces.Trace(ctx, store.TraceCall{LaunchID: other.ID, At: time.Now(), Provider: "meta", Status: 200, Question: "not yours"})
	r, err := redact.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := redact.LoadSettings(ctx, s.Meta())
	if err != nil {
		t.Fatal(err)
	}
	o := BuiltinOptions{
		Version:    "1.2.3",
		Runners:    func() []string { return []string{"zeta", "alpha"} },
		Providers:  func() []string { return []string{"meta"} },
		Launches:   tokens,
		TraceStore: traces,
		Terminals: func(_ context.Context, runner string) ([]terminals.TermInfo, error) {
			if termErr != nil {
				return nil, termErr
			}
			if runner == "empty" {
				return nil, nil
			}
			return []terminals.TermInfo{{Term: "t1", Created: 1}}, nil
		},
		Redact: r, RedactSettings: settings,
	}
	f := &builtinFixture{tools: map[string]Tool{}, caller: Caller{LaunchID: mine.ID, Label: "mine", Provider: "meta"}, settings: settings}
	for _, tool := range Builtins(o) {
		f.tools[tool.Name] = tool
	}
	return f
}

// run calls a tool and returns its result as JSON for easy checks.
func (f *builtinFixture) run(t *testing.T, name, args string) (Result, map[string]any) {
	t.Helper()
	res, err := f.tools[name].Call(context.Background(), f.caller, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var m map[string]any
	if res.Structured != nil {
		b, _ := json.Marshal(res.Structured)
		_ = json.Unmarshal(b, &m)
	}
	return res, m
}

func TestBuiltinsRegisterAndAreReadOnly(t *testing.T) {
	r := NewRegistry()
	tools := Builtins(BuiltinOptions{})
	if err := r.Register(tools...); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range r.sorted() {
		names = append(names, tool.Name)
		if !tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint || tool.Annotations.OpenWorldHint {
			t.Fatalf("%s annotations = %+v", tool.Name, tool.Annotations)
		}
	}
	if strings.Join(names, ",") != "check_redaction,launch_info,list_terminals,my_trace,status" {
		t.Fatalf("builtins = %v", names)
	}
}

func TestStatusTool(t *testing.T) {
	f := newBuiltins(t, nil)
	_, m := f.run(t, "status", "")
	if m["version"] != "1.2.3" || len(m["runners"].([]any)) != 2 || m["runners"].([]any)[0] != "alpha" || m["providers"].([]any)[0] != "meta" {
		t.Fatalf("status = %v", m)
	}
	if pol, _ := m["redaction"].(map[string]any); pol["SECRET"] != "mask" {
		t.Fatalf("redaction = %v", m["redaction"])
	}
	if res, _ := f.run(t, "status", `{"x":1}`); !res.IsError {
		t.Fatal("status with arguments must fail")
	}
}

func TestLaunchInfoTool(t *testing.T) {
	f := newBuiltins(t, nil)
	_, m := f.run(t, "launch_info", "{}")
	if m["id"] != f.caller.LaunchID || m["label"] != "mine" || m["provider"] != "meta" || m["active"] != true {
		t.Fatalf("launch_info = %v", m)
	}
	if res, _ := f.run(t, "launch_info", `{"x":1}`); !res.IsError {
		t.Fatal("launch_info with arguments must fail")
	}
	f.caller.LaunchID = "gone"
	if res, _ := f.run(t, "launch_info", ""); !res.IsError || !strings.Contains(res.Text, "no longer") {
		t.Fatalf("unknown launch = %+v", res)
	}
}

func TestMyTraceTool(t *testing.T) {
	f := newBuiltins(t, nil)
	_, m := f.run(t, "my_trace", "")
	calls, _ := m["calls"].([]any)
	if m["total"] != float64(3) || len(calls) != 3 || m["launchId"] != f.caller.LaunchID {
		t.Fatalf("my_trace = %v", m)
	}
	_, m = f.run(t, "my_trace", `{"limit":2}`)
	calls, _ = m["calls"].([]any)
	if len(calls) != 2 || calls[1].(map[string]any)["question"] != "q2" {
		t.Fatalf("limited = %v", m)
	}
	b, _ := json.Marshal(m)
	if strings.Contains(string(b), "not yours") {
		t.Fatal("my_trace showed another launch's calls")
	}
	for _, bad := range []string{`{"limit":0.5}`, `{"limit":101}`, `{"limit":-1}`, `{"who":"x"}`} {
		if res, _ := f.run(t, "my_trace", bad); !res.IsError {
			t.Fatalf("my_trace %s accepted", bad)
		}
	}
}

func TestListTerminalsTool(t *testing.T) {
	f := newBuiltins(t, nil)
	_, m := f.run(t, "list_terminals", "")
	if list, _ := m["terminals"].([]any); len(list) != 1 || list[0].(map[string]any)["term"] != "t1" {
		t.Fatalf("list_terminals = %v", m)
	}
	if _, m = f.run(t, "list_terminals", `{"runner":"empty"}`); m["terminals"] == nil || len(m["terminals"].([]any)) != 0 {
		t.Fatalf("empty = %v", m)
	}
	if res, _ := f.run(t, "list_terminals", `{"runner":1}`); !res.IsError {
		t.Fatal("a numeric runner must fail")
	}
	f = newBuiltins(t, errors.New("no runner linked"))
	if res, _ := f.run(t, "list_terminals", ""); !res.IsError || res.Text != "no runner linked" {
		t.Fatalf("relay error = %+v", res)
	}
}

func TestCheckRedactionTool(t *testing.T) {
	f := newBuiltins(t, nil)
	_, m := f.run(t, "check_redaction", `{"text":"key `+fakeAWS+` here"}`)
	if text, _ := m["text"].(string); strings.Contains(text, fakeAWS) || !strings.Contains(text, "here") {
		t.Fatalf("masked = %v", m)
	}
	if fs, _ := m["findings"].([]any); len(fs) != 1 || len(m["blocked"].([]any)) != 0 {
		t.Fatalf("findings = %v", m)
	}
	p := redact.DefaultPolicy()
	p[redact.Secret] = redact.Block
	if err := f.settings.SetPolicy(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	_, m = f.run(t, "check_redaction", `{"text":"key `+fakeAWS+`"}`)
	if m["text"] != "" || len(m["blocked"].([]any)) != 1 {
		t.Fatalf("blocked = %v", m)
	}
	for _, bad := range []string{`{}`, `{"text":1}`, `{"text":"x","y":1}`, `{"text":"` + strings.Repeat("a", maxCheckText+1) + `"}`} {
		if res, _ := f.run(t, "check_redaction", bad); !res.IsError {
			t.Fatalf("check_redaction %.40s accepted", bad)
		}
	}
}

func TestBuiltinsNotConfigured(t *testing.T) {
	tools := map[string]Tool{}
	for _, tool := range Builtins(BuiltinOptions{}) {
		tools[tool.Name] = tool
	}
	for name, args := range map[string]string{"launch_info": "", "my_trace": "", "list_terminals": "", "check_redaction": `{"text":"x"}`} {
		res, err := tools[name].Call(context.Background(), Caller{LaunchID: "x"}, json.RawMessage(args))
		if err != nil || !res.IsError || !strings.Contains(res.Text, "not configured") {
			t.Fatalf("%s = %+v %v", name, res, err)
		}
	}
	res, err := tools["status"].Call(context.Background(), Caller{}, nil)
	if err != nil || res.IsError {
		t.Fatalf("status = %+v %v", res, err)
	}
	b, _ := json.Marshal(res.Structured)
	if string(b) != `{"providers":[],"runners":[],"version":""}` {
		t.Fatalf("bare status = %s", b)
	}
}
