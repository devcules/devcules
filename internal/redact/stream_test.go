package redact

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// restoreChunked feeds reply to a fresh restorer in the given chunk
// sizes and returns everything it emitted.
func restoreChunked(s *Session, reply string, sizes []int) string {
	sr := s.Restorer()
	var b strings.Builder
	for len(reply) > 0 {
		n := 1
		if len(sizes) > 0 {
			n, sizes = max(1, sizes[0]), sizes[1:]
		}
		n = min(n, len(reply))
		b.WriteString(sr.Write(reply[:n]))
		reply = reply[n:]
	}
	b.WriteString(sr.Flush())
	return b.String()
}

func maskedSession(t testing.TB) (*Session, string) {
	t.Helper()
	r := newTestRedactor(t)
	s := r.NewTestSession(Policy{Secret: Mask, Email: Mask, Phone: Mask, Card: Off, IPv4: Mask})
	out, f, err := s.Redact("key " + fakeAWS + " for ada@example.org at 8.8.8.8")
	if err != nil || len(f) != 3 {
		t.Fatalf("setup: %q %v %v", out, f, err)
	}
	return s, out
}

func TestStreamRestoreSplitPlaceholder(t *testing.T) {
	s, masked := maskedSession(t)
	want := s.Restore(masked)
	for size := 1; size <= len(masked); size++ {
		sizes := make([]int, len(masked))
		for i := range sizes {
			sizes[i] = size
		}
		if got := restoreChunked(s, masked, sizes); got != want {
			t.Fatalf("chunk size %d: got %q, want %q", size, got, want)
		}
	}
}

func TestStreamHoldsOnlyWhatItMust(t *testing.T) {
	s, _ := maskedSession(t)
	sr := s.Restorer()
	if got := sr.Write("plain text, no R in sight"); got != "plain text, no R in sight" {
		t.Fatalf("plain text held: %q", got)
	}
	if got := sr.Write("Roger said REDAC"); got != "Roger said " {
		t.Fatalf("got %q, want the text before the partial placeholder", got)
	}
	if got := sr.Write("TION follows"); got != "REDACTION follows" {
		t.Fatalf("a non-placeholder was not released: %q", got)
	}
	if got := sr.Write(" REDACTED_EMAIL_01"); got != " " {
		t.Fatalf("got %q", got)
	}
	if got := sr.Flush(); got != "REDACTED_EMAIL_01" {
		t.Fatalf("flush: %q", got)
	}
}

func TestIsPlaceholderPrefix(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"R", true}, {"REDACTED_", true}, {"REDACTED_EMAIL", true}, {"REDACTED_EMAIL_", true},
		{"REDACTED_EMAIL_0123ABCDE", true}, {"REDACTED_EMAIL_0123ABCDEF", false},
		{"REDACTED_EMAIL_01g", false}, {"REDACTED__", false}, {"REDACTION", false},
		{"REDACTED_" + strings.Repeat("A", 17), false}, {"X", false},
	} {
		if got := isPlaceholderPrefix(tc.in); got != tc.want {
			t.Errorf("isPlaceholderPrefix(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// FuzzStreamRestore: however a reply is chunked, the streamed restore
// equals restoring the whole reply at once.
func FuzzStreamRestore(f *testing.F) {
	f.Add("before REDACTED_EMAIL_0123456789 after", uint64(1))
	f.Add("RRREDACTED_REDACTED_SECRET_", uint64(2))
	f.Add("", uint64(3))
	s, masked := maskedSession(f)
	f.Add(masked, uint64(4))
	f.Add(masked+masked[:20], uint64(5))
	f.Fuzz(func(t *testing.T, reply string, seed uint64) {
		// Mix the session's real placeholders into the reply so the
		// fuzzer exercises restores, not just pass-through.
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		var phs []string
		for ph := range s.values {
			phs = append(phs, ph)
		}
		if len(phs) > 0 && len(reply) > 0 {
			at := rng.IntN(len(reply) + 1)
			reply = reply[:at] + phs[rng.IntN(len(phs))] + reply[at:]
		}
		sizes := make([]int, 64)
		for i := range sizes {
			sizes[i] = 1 + rng.IntN(40)
		}
		want := s.Restore(reply)
		if got := restoreChunked(s, reply, sizes); got != want {
			t.Fatalf("chunked restore differs:\n got %q\nwant %q", got, want)
		}
	})
}
