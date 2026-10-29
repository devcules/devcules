package router

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/devcules/devcules/internal/redact"
)

// structuralKeys hold ids, enums and opaque tokens, never prose: they
// are not scanned, so a redaction can never break a protocol field
// (a tool call id, a thinking signature, encrypted reasoning).
var structuralKeys = map[string]bool{
	"model": true, "role": true, "type": true, "id": true, "object": true,
	"tool_call_id": true, "tool_use_id": true, "call_id": true, "item_id": true,
	"previous_response_id": true, "signature": true, "encrypted_content": true,
	"finish_reason": true, "stop_reason": true, "media_type": true,
}

// decodeJSON parses a body keeping numbers exact.
func decodeJSON(body []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data after the JSON value")
	}
	return v, nil
}

// encodeJSON writes v without HTML escaping, so text round-trips as
// the client sent it.
func encodeJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// redactValue redacts every prose string in v in place and returns the
// findings. Data URIs (inline images and files) are skipped: masking
// inside base64 would corrupt the file, not protect a secret. A string
// in block mode is collected; the whole request then fails with every
// blocked type named.
func redactValue(v any, s *redact.Session) (any, []redact.Finding, error) {
	var findings []redact.Finding
	var blocked []redact.Type
	var walk func(v any, key string) any
	walk = func(v any, key string) any {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				x[k] = walk(child, k)
			}
			return x
		case []any:
			for i, child := range x {
				x[i] = walk(child, key)
			}
			return x
		case string:
			if structuralKeys[key] || strings.HasPrefix(x, "data:") {
				return x
			}
			out, f, err := s.Redact(x)
			findings = append(findings, f...)
			var be *redact.BlockedError
			if errors.As(err, &be) {
				for _, t := range be.Types {
					if !slices.Contains(blocked, t) {
						blocked = append(blocked, t)
					}
				}
				return x
			}
			return out
		default:
			return v
		}
	}
	v = walk(v, "")
	if len(blocked) > 0 {
		return nil, findings, &redact.BlockedError{Types: blocked}
	}
	return v, findings, nil
}

// restoreValue puts this session's placeholders back into every string
// in v, in place.
func restoreValue(v any, s *redact.Session) any {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			x[k] = restoreValue(child, s)
		}
		return x
	case []any:
		for i, child := range x {
			x[i] = restoreValue(child, s)
		}
		return x
	case string:
		return s.Restore(x)
	default:
		return v
	}
}
