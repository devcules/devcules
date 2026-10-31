package assistants

import (
	"errors"

	"github.com/devcules/devcules/internal/redact"
)

// SessionRedact is RedactIO over one redaction session: the settings'
// policy, same sessions the router uses, so user messages are masked
// with the same placeholders a launched agent's traffic is.
type SessionRedact struct {
	s *redact.Session
}

// NewSessionRedact starts the round trip for one assistant over the
// settings' current policy.
func NewSessionRedact(r *redact.Redactor, s *redact.Settings) *SessionRedact {
	return &SessionRedact{s: r.NewSession(s.Policy())}
}

// RedactIn implements RedactIO: a block-mode finding rejects the whole
// message with a readable reason.
func (a *SessionRedact) RedactIn(text string) (string, error) {
	out, _, err := a.s.Redact(text)
	if err != nil {
		var b *redact.BlockedError
		if errors.As(err, &b) {
			return "", b
		}
		return "", err
	}
	return out, nil
}
