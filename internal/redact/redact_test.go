package redact

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// testKey is a fixed placeholder key, so golden files are stable.
var testKey = bytes.Repeat([]byte{0x42}, 32)

func newTestRedactor(t testing.TB) *Redactor {
	t.Helper()
	r, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// only masks one type and turns the rest off.
func only(typ Type, m Mode) Policy {
	p := Policy{}
	for _, t := range Types {
		p[t] = Off
	}
	p[typ] = m
	return p
}

// fakeAWS is a made-up access key id in the AWS shape, not a real one.
const fakeAWS = "AKIA" + "Z7QWERTY4UIOP3XZ"

func TestNewKeyLength(t *testing.T) {
	if _, err := New(make([]byte, 31)); err == nil {
		t.Fatal("a 31 byte key was accepted")
	}
}

func TestPolicyValidate(t *testing.T) {
	if err := DefaultPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
	p := DefaultPolicy()
	p["SSN"] = Mask
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), `unknown type "SSN"`) {
		t.Fatalf("unknown type: %v", err)
	}
	p = DefaultPolicy()
	p[Email] = "hide"
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), `unknown mode "hide"`) {
		t.Fatalf("unknown mode: %v", err)
	}
	p = DefaultPolicy()
	delete(p, Card)
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "no mode for CARD") {
		t.Fatalf("missing type: %v", err)
	}
}

func TestModes(t *testing.T) {
	r := newTestRedactor(t)
	text := "key " + fakeAWS + " mail ada@example.org"

	out, f, err := r.NewTestSession(only(Secret, Off)).Redact(text)
	if err != nil || out != text || len(f) != 0 {
		t.Fatalf("off: %q %v %v", out, f, err)
	}

	out, f, err = r.NewTestSession(only(Secret, Log)).Redact(text)
	if err != nil || out != text || len(f) != 1 || f[0].Placeholder != "" || f[0].Type != Secret {
		t.Fatalf("log: %q %v %v", out, f, err)
	}

	out, f, err = r.NewTestSession(only(Secret, Mask)).Redact(text)
	if err != nil || strings.Contains(out, fakeAWS) || len(f) != 1 || !strings.Contains(out, f[0].Placeholder) {
		t.Fatalf("mask: %q %v %v", out, f, err)
	}
	if f[0].Rule != "aws-access-token" {
		t.Fatalf("rule = %q, want aws-access-token", f[0].Rule)
	}

	out, f, err = r.NewTestSession(only(Secret, Block)).Redact(text)
	var be *BlockedError
	if out != "" || len(f) != 1 || !errors.As(err, &be) || !errors.Is(err, ErrBlocked) {
		t.Fatalf("block: %q %v %v", out, f, err)
	}
	if strings.Contains(err.Error(), fakeAWS) || !strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("block error must name the type, never the value: %v", err)
	}
	if f[0].Placeholder != "" {
		t.Fatal("a blocked finding carries a placeholder")
	}
}

func TestMixedPolicy(t *testing.T) {
	r := newTestRedactor(t)
	p := Policy{Secret: Mask, Email: Mask, Phone: Log, Card: Off, IPv4: Mask}
	text := "aws " + fakeAWS + ", ada@example.org, +44 20 7946 0958, 8.8.8.8 and 10.0.0.1"
	out, f, err := r.NewTestSession(p).Redact(text)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{fakeAWS, "ada@example.org", "8.8.8.8"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q still in %q", gone, out)
		}
	}
	for _, kept := range []string{"+44 20 7946 0958", "10.0.0.1"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%q missing from %q", kept, out)
		}
	}
	if len(f) != 4 {
		t.Fatalf("findings = %v, want 4", f)
	}
}

func TestPlaceholderStable(t *testing.T) {
	r := newTestRedactor(t)
	a, b := r.placeholder(Email, "ada@example.org"), r.placeholder(Email, "ada@example.org")
	if a != b {
		t.Fatal("same value, different placeholders")
	}
	if !placeholderRE.MatchString(a) || placeholderRE.FindString(a) != a {
		t.Fatalf("%q does not match the placeholder grammar", a)
	}
	if r.placeholder(Phone, "ada@example.org") == a {
		t.Fatal("the type is not bound into the placeholder")
	}
	other, _ := New(bytes.Repeat([]byte{1}, 32))
	if other.placeholder(Email, "ada@example.org") == a {
		t.Fatal("the key is not bound into the placeholder")
	}
	out, _, _ := r.NewTestSession(only(Email, Mask)).Redact("ada@example.org ada@example.org")
	if out != a+" "+a {
		t.Fatalf("repeats: %q", out)
	}
}

func TestRestoreOnlyIssued(t *testing.T) {
	r := newTestRedactor(t)
	s := r.NewTestSession(only(Email, Mask))
	out, f, _ := s.Redact("write to ada@example.org")
	ph := f[0].Placeholder
	fake := "REDACTED_EMAIL_0123456789"
	reply := "Sent to " + ph + ", not " + fake + "."
	if got := s.Restore(reply); got != "Sent to ada@example.org, not "+fake+"." {
		t.Fatalf("restore: %q", got)
	}
	if s2 := r.NewTestSession(only(Email, Mask)); s2.Restore(out) != out {
		t.Fatal("a fresh session restored a placeholder it never issued")
	}
}

func TestOverlapsPreferEarliestLongest(t *testing.T) {
	got := resolveOverlaps([]span{
		{10, 20, Email, "email"},
		{0, 5, Secret, "a"},
		{0, 8, Secret, "b"},
		{4, 12, Phone, "phone"},
		{20, 25, IPv4, "ipv4"},
	})
	want := []span{{0, 8, Secret, "b"}, {10, 20, Email, "email"}, {20, 25, IPv4, "ipv4"}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestCounters(t *testing.T) {
	r := newTestRedactor(t)
	p := Policy{Secret: Mask, Email: Log, Phone: Off, Card: Block, IPv4: Off}
	s := r.NewSession(p)
	if _, _, err := s.Redact(fakeAWS + " ada@example.org"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Redact("4111 1111 1111 1111"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("card not blocked: %v", err)
	}
	r.NewTestSession(p).Redact(fakeAWS) // the test box does not count
	st := r.Stats()
	want := map[Type]Counts{
		Secret: {Detected: 1, Masked: 1},
		Email:  {Detected: 1},
		Card:   {Detected: 1, Blocked: 1},
	}
	for _, typ := range Types {
		if st[typ] != want[typ] {
			t.Errorf("%s: got %+v, want %+v", typ, st[typ], want[typ])
		}
	}
}

func TestConcurrentSessions(t *testing.T) {
	r := newTestRedactor(t)
	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			s := r.NewSession(DefaultPolicy())
			for range 50 {
				out, _, _ := s.Redact("k " + fakeAWS)
				_ = s.Restore(out)
			}
		}()
	}
	for range 8 {
		<-done
	}
	if got := r.Stats()[Secret].Masked; got != 400 {
		t.Fatalf("masked = %d, want 400", got)
	}
}

// TestGolden runs each detector over testdata/<type>.txt in mask mode
// and compares with <type>.golden. Inputs may split a value with %%,
// which the test deletes first, so key-shaped fixtures never sit whole
// in the repository. Run `go test -update` after a
// deliberate detector change and review the diff.
func TestGolden(t *testing.T) {
	r := newTestRedactor(t)
	for _, typ := range append([]Type{Secret}, piiTypes()...) {
		t.Run(string(typ), func(t *testing.T) {
			name := strings.ToLower(string(typ))
			in, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
			if err != nil {
				t.Fatal(err)
			}
			var got strings.Builder
			for _, line := range strings.Split(strings.TrimRight(string(in), "\n"), "\n") {
				out, _, err := r.NewTestSession(only(typ, Mask)).Redact(strings.ReplaceAll(line, "%%", ""))
				if err != nil {
					t.Fatal(err)
				}
				got.WriteString(out + "\n")
			}
			golden := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.WriteFile(golden, []byte(got.String()), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got.String() != string(want) {
				t.Fatalf("%s differs from %s:\n%s", name+".txt", golden, diffLines(string(want), got.String()))
			}
		})
	}
}

func piiTypes() []Type {
	var out []Type
	for _, d := range piiDetectors {
		out = append(out, d.typ)
	}
	return out
}

func diffLines(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var b strings.Builder
	for i := 0; i < max(len(w), len(g)); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			b.WriteString("- " + wl + "\n+ " + gl + "\n")
		}
	}
	return b.String()
}
