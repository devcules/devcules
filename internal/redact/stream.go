package redact

import "strings"

const placeholderPrefix = "REDACTED_"

// StreamRestorer restores placeholders in a reply that arrives in
// chunks. A placeholder can be split across chunks, so it holds back a
// tail that could still grow into one; everything else passes straight
// through.
type StreamRestorer struct {
	s       *Session
	pending string
}

// Restorer returns a streaming restorer for this session's reply.
func (s *Session) Restorer() *StreamRestorer {
	return &StreamRestorer{s: s}
}

// Write takes the next chunk and returns the text that is safe to emit
// now, restored.
func (sr *StreamRestorer) Write(chunk string) string {
	buf := sr.pending + chunk
	cut := holdFrom(buf)
	sr.pending = buf[cut:]
	return sr.s.Restore(buf[:cut])
}

// Flush returns whatever is held back, restored. Call it when the
// reply ends.
func (sr *StreamRestorer) Flush() string {
	out := sr.s.Restore(sr.pending)
	sr.pending = ""
	return out
}

// holdFrom returns the index from which buf's tail might be the start
// of an unfinished placeholder; len(buf) when nothing needs holding.
func holdFrom(buf string) int {
	// The earliest candidate wins: a held tail that starts at an "R"
	// must be checked against the whole grammar, not just its prefix.
	for i := max(0, len(buf)-placeholderMaxLen); i < len(buf); i++ {
		if buf[i] == 'R' && isPlaceholderPrefix(buf[i:]) {
			return i
		}
	}
	return len(buf)
}

// placeholderMaxLen bounds how far back a partial placeholder can
// start: the prefix, a type name, the separator and 10 hex digits.
const placeholderMaxLen = len(placeholderPrefix) + 16 + 1 + 10

// isPlaceholderPrefix reports whether s is a proper prefix of some
// placeholder, so more input could complete it.
func isPlaceholderPrefix(s string) bool {
	if len(s) <= len(placeholderPrefix) {
		return strings.HasPrefix(placeholderPrefix, s)
	}
	if !strings.HasPrefix(s, placeholderPrefix) {
		return false
	}
	rest := s[len(placeholderPrefix):]
	i := 0
	for i < len(rest) && isTypeByte(rest[i]) {
		i++
	}
	if i == len(rest) {
		return i <= 16
	}
	if i == 0 || rest[i] != '_' {
		return false
	}
	hex := rest[i+1:]
	if len(hex) >= 10 {
		return false
	}
	for j := 0; j < len(hex); j++ {
		if !isUpperHex(hex[j]) {
			return false
		}
	}
	return true
}

func isTypeByte(c byte) bool { return (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') }

func isUpperHex(c byte) bool { return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') }
