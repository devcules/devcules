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
