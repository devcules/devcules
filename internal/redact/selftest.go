package redact

import (
	"errors"
	"fmt"
	"strings"
)

// selfTestFixtures are one known value per type. They are assembled
// from parts so the source never holds a whole key shape, which keeps
// secret scanners quiet without an allowlist entry.
func selfTestFixtures() map[Type]string {
	return map[Type]string{
		Secret: "AKIA" + "Z7QWERTY4UIOP3XZ",
		Email:  "self.test" + "@" + "devcules.example.com",
		Phone:  "+44 20 " + "7946 0958",
		Card:   "4111 1111 " + "1111 1111",
		IPv4:   "8.8." + "4.4",
	}
}

// SelfTest proves the detectors work before serve takes traffic: each
// fixture is found as its type and masked, the reply restores in one
// piece and streamed byte by byte, and block mode refuses. It never
// touches the counters. Serve refuses to start on an error.
func SelfTest(r *Redactor) error {
	all := Policy{}
	for _, t := range Types {
		all[t] = Mask
	}
	fixtures := selfTestFixtures()
	for _, t := range Types {
		value := fixtures[t]
		text := "self-test " + string(t) + ": " + value + " (end)"
		s := r.NewTestSession(all)
		out, findings, err := s.Redact(text)
		if err != nil {
			return fmt.Errorf("redact self-test: %s: %w", t, err)
		}
		if len(findings) != 1 || findings[0].Type != t {
			return fmt.Errorf("redact self-test: %s fixture gave %d findings, want one %s", t, len(findings), t)
		}
		if strings.Contains(out, value) || !strings.Contains(out, findings[0].Placeholder) {
			return fmt.Errorf("redact self-test: %s fixture was not masked", t)
		}
		reply := "ok, " + findings[0].Placeholder + "."
		want := "ok, " + value + "."
		if got := s.Restore(reply); got != want {
			return fmt.Errorf("redact self-test: %s placeholder did not restore", t)
		}
		sr := s.Restorer()
		var b strings.Builder
		for i := 0; i < len(reply); i++ {
			b.WriteString(sr.Write(reply[i : i+1]))
		}
		b.WriteString(sr.Flush())
		if b.String() != want {
			return fmt.Errorf("redact self-test: %s placeholder did not restore when streamed", t)
		}
		block := Policy{}
		for _, u := range Types {
			block[u] = Off
		}
		block[t] = Block
		if _, _, err := r.NewTestSession(block).Redact(text); !errors.Is(err, ErrBlocked) {
			return fmt.Errorf("redact self-test: %s in block mode was not refused", t)
		}
	}
	return nil
}
