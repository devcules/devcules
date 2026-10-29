package agents

import (
	"strings"
	"testing"
)

func TestPreToolUseShapes(t *testing.T) {
	for in, want := range map[string]string{
		`{"tool_name":"shell","tool_input":{"command":"ls -la"}}`:                                   "ls -la",
		`{"tool_name":"shell","tool_input":{"command":["git","status"]}}`:                           "git status",
		`{"tool_name":"shell","tool_input":{"command":7}}`:                                          `shell {"command":7}`,
		`{"tool_name":"Edit","tool_input":{"file_path":"x.go"}}`:                                    `Edit {"file_path":"x.go"}`,
		`{"tool_name":"Custom","tool_input":"raw text"}`:                                            "Custom raw text",
		`{"tool_name":"Empty"}`:                                                                     "Empty",
		`{"tool_name":"Big","tool_input":{"command":"` + strings.Repeat("a", MaxCommand+10) + `"}}`: strings.Repeat("a", MaxCommand),
	} {
		c, err := PreToolUse("x", []byte(in))
		if err != nil || c.Command != want {
			t.Errorf("PreToolUse(%.60s) = %.80q %v, want %.80q", in, c.Command, err, want)
		}
	}
	for _, bad := range []string{`{"hook_event_name":"PostToolUse","tool_name":"Bash"}`, `{}`, `nope`} {
		if _, err := PreToolUse("x", []byte(bad)); err == nil || !strings.HasPrefix(err.Error(), "x: ") {
			t.Errorf("PreToolUse(%s) = %v, want an error naming the agent", bad, err)
		}
	}
}

func TestBlockedAndSemVer(t *testing.T) {
	if r := Blocked(Answer{}); r.Code != 2 || r.Stderr != "Devcules blocked this call: denied" {
		t.Fatalf("bare deny = %+v", r)
	}
	if r := Blocked(Answer{Reason: "no"}); r.Code != 2 || r.Stderr != "Devcules blocked this call: no" {
		t.Fatalf("deny = %+v", r)
	}
	if r := Blocked(Answer{Failed: true, Reason: "down"}); r.Code != 2 || !strings.HasSuffix(r.Stderr, "so it is blocked: down") {
		t.Fatalf("failed = %+v", r)
	}
	for in, want := range map[string]string{
		"Muse Code 1.4.14 (1.4.14-R7309.1)": "1.4.14",
		"2.1.211 (Claude Code)":             "2.1.211",
		"codex-cli 0.143.0":                 "0.143.0",
		"dev":                               "dev",
	} {
		if got := SemVer(in); got != want {
			t.Errorf("SemVer(%q) = %q, want %q", in, got, want)
		}
	}
}
