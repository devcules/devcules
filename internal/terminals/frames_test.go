package terminals

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestValidIDTable pins the id shape both ends enforce: anything but
// 16 lowercase hex is rejected before it can reach tmux.
func TestValidIDTable(t *testing.T) {
	t.Parallel()
	valid := []string{"0123456789abcdef", "ffffffffffffffff", "0000000000000000"}
	for _, id := range valid {
		if !ValidID(id) {
			t.Fatalf("ValidID(%q) = false, want true", id)
		}
	}
	invalid := []string{
		"", "abc", "0123456789abcde", "0123456789abcdef0",
		"0123456789ABCDEF", "dvc-0123456789ab", "0123456789abcde!",
		" 0123456789abcdef", "0123456789abcdef ",
	}
	for _, id := range invalid {
		if ValidID(id) {
			t.Fatalf("ValidID(%q) = true, want false", id)
		}
	}
	if SessionName("abc123abc123abc1") != "dvc-abc123abc123abc1" {
		t.Fatal("SessionName lost the prefix")
	}
	seen := map[string]bool{}
	for range 100 {
		id := NewID()
		if !ValidID(id) {
			t.Fatalf("NewID() = %q fails validation", id)
		}
		if seen[id] {
			t.Fatalf("NewID() collision on %q", id)
		}
		seen[id] = true
	}
}

// TestValidSizeTable pins the window bounds: 0x0 and gigapixel both fail.
func TestValidSizeTable(t *testing.T) {
	t.Parallel()
	for _, s := range [][2]int{{1, 1}, {80, 24}, {500, 500}, {390, 100}} {
		if !ValidSize(s[0], s[1]) {
			t.Fatalf("ValidSize(%d, %d) = false, want true", s[0], s[1])
		}
	}
	for _, s := range [][2]int{{0, 24}, {80, 0}, {-1, 24}, {501, 24}, {80, 501}, {100000, 100000}} {
		if ValidSize(s[0], s[1]) {
			t.Fatalf("ValidSize(%d, %d) = true, want false", s[0], s[1])
		}
	}
}

// TestCheckers walks every frame validator through its happy path and
// each rejection, so runner and relay share exact error behavior.
func TestCheckers(t *testing.T) {
	t.Parallel()
	id := "0123456789abcdef"
	if err := CheckOpen(Open{Term: id}); err != nil {
		t.Fatalf("CheckOpen: %s", err)
	}
	if err := CheckOpen(Open{Term: "bad"}); err == nil {
		t.Fatal("CheckOpen(bad) succeeded")
	}
	if err := CheckAttach(Attach{Term: id, Stream: id, Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("CheckAttach: %s", err)
	}
	for _, a := range []Attach{
		{Term: "bad", Stream: id, Cols: 80, Rows: 24},
		{Term: id, Stream: "bad", Cols: 80, Rows: 24},
		{Term: id, Stream: id, Cols: 0, Rows: 24},
		{Term: id, Stream: id, Cols: 80, Rows: 501},
	} {
		if err := CheckAttach(a); err == nil {
			t.Fatalf("CheckAttach(%+v) succeeded", a)
		}
	}
	b64 := base64.StdEncoding.EncodeToString([]byte("hi"))
	if _, err := DecodeInput(Input{Stream: id, B64: b64}); err != nil {
		t.Fatalf("DecodeInput: %s", err)
	}
	if _, err := DecodeInput(Input{Stream: "bad", B64: b64}); err == nil {
		t.Fatal("DecodeInput(bad stream) succeeded")
	}
	if _, err := DecodeInput(Input{Stream: id, B64: "!!!"}); err == nil {
		t.Fatal("DecodeInput(bad b64) succeeded")
	}
	big := base64.StdEncoding.EncodeToString(make([]byte, MaxInputBytes+1))
	if _, err := DecodeInput(Input{Stream: id, B64: big}); err == nil {
		t.Fatal("DecodeInput(oversize) succeeded")
	} else if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("DecodeInput(oversize) = %q, want the ceiling with next steps", err)
	}
	if err := CheckResize(Resize{Stream: id, Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("CheckResize: %s", err)
	}
	if err := CheckResize(Resize{Stream: id, Cols: 0, Rows: 24}); err == nil {
		t.Fatal("CheckResize(0 cols) succeeded")
	}
	if err := CheckStream(id); err != nil {
		t.Fatalf("CheckStream: %s", err)
	}
	if err := CheckStream("bad"); err == nil {
		t.Fatal("CheckStream(bad) succeeded")
	}
	if err := CheckClose(Close{Term: id}); err != nil {
		t.Fatalf("CheckClose: %s", err)
	}
	if err := CheckClose(Close{Term: "bad"}); err == nil {
		t.Fatal("CheckClose(bad) succeeded")
	}
	if _, err := DecodeOutput(Output{Stream: id, B64: b64}); err != nil {
		t.Fatalf("DecodeOutput: %s", err)
	}
	if _, err := DecodeOutput(Output{Stream: id, B64: "!!!"}); err == nil {
		t.Fatal("DecodeOutput(bad b64) succeeded")
	}
}

func TestCheckOpenAgent(t *testing.T) {
	id := "0123456789abcdef"
	ok := AgentLaunch{ID: "musecode", Token: "dvl_abc", Model: "muse-spark-1.3"}
	if err := CheckOpen(Open{Term: id, Agent: &ok}); err != nil {
		t.Fatalf("CheckOpen(agent): %s", err)
	}
	for name, a := range map[string]AgentLaunch{
		"upper id":    {ID: "MuseCode", Token: "t"},
		"empty id":    {Token: "t"},
		"no token":    {ID: "musecode"},
		"space token": {ID: "musecode", Token: "a b"},
		"bad model":   {ID: "musecode", Token: "t", Model: "x; rm -rf /"},
	} {
		if err := CheckOpen(Open{Term: id, Agent: &a}); err == nil {
			t.Errorf("%s: CheckOpen succeeded", name)
		}
	}
}

func TestCheckOpenPlace(t *testing.T) {
	id := "0123456789abcdef"
	for _, o := range []Open{
		{Term: id, Dir: "shop"},
		{Term: id, Dir: "/Users/k/src/shop", Project: "p_abc123"},
		{Term: id, Dir: "shop", Clone: "https://github.com/acme/shop.git"},
		{Term: id, Dir: "shop", Clone: "git@github.com:acme/shop.git"},
		{Term: id, Dir: "src/shop", Clone: "ssh://git@host:2222/acme/shop"},
	} {
		if err := CheckOpen(o); err != nil {
			t.Errorf("CheckOpen(%+v): %s", o, err)
		}
	}
	for name, o := range map[string]Open{
		"dotdot":        {Term: id, Dir: "../etc"},
		"inner dotdot":  {Term: id, Dir: "/a/../etc"},
		"dot prefix":    {Term: id, Dir: "./shop"},
		"newline":       {Term: id, Dir: "shop\nrm"},
		"long":          {Term: id, Dir: strings.Repeat("a", 513)},
		"bad project":   {Term: id, Dir: "shop", Project: "P_UPPER"},
		"clone no dir":  {Term: id, Clone: "https://github.com/acme/shop.git"},
		"clone flag":    {Term: id, Dir: "shop", Clone: "--upload-pack=touch /tmp/x"},
		"clone file":    {Term: id, Dir: "shop", Clone: "file:///etc"},
		"clone space":   {Term: id, Dir: "shop", Clone: "https://a b"},
		"clone http":    {Term: id, Dir: "shop", Clone: "http://example.com/x.git"},
		"clone ext":     {Term: id, Dir: "shop", Clone: "ext::sh -c touch% /tmp/x"},
		"clone newline": {Term: id, Dir: "shop", Clone: "https://example.com/x\n.git"},
	} {
		if err := CheckOpen(o); err == nil {
			t.Errorf("%s: CheckOpen(%+v) succeeded", name, o)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, n := range []string{"api server", "Muse · shop", "x"} {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false", n)
		}
	}
	for _, n := range []string{"", " lead", "trail ", "tab\there", "nl\n", strings.Repeat("a", 41)} {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true", n)
		}
	}
	if err := CheckOpen(Open{Term: "0123456789abcdef", Name: "bad\tname"}); err == nil {
		t.Error("CheckOpen accepted a bad name")
	}
}

func TestDetectRemote(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/acme/shop.git":       "github",
		"git@github.com:acme/shop.git":           "github",
		"https://gitlab.com/acme/shop.git":       "gitlab",
		"ssh://git@gitlab.acme.internal/group":   "gitlab",
		"https://dev.azure.com/acme/team/_git/x": "azure",
		"git@ssh.dev.azure.com:v3/acme/team/x":   "azure",
		"https://acme.visualstudio.com/x":        "azure",
		"https://codeberg.org/acme/shop":         "",
		"git@host:acme/plain.git":                "",
		"":                                       "",
	} {
		if got := DetectRemote(url); got != want {
			t.Errorf("DetectRemote(%q) = %q, want %q", url, got, want)
		}
	}
}
