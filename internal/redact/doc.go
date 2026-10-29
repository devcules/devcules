// Package redact finds secrets and personal data in text bound for a
// model, swaps them for deterministic placeholders, and restores them in
// the reply, streamed replies included.
//
// Detectors come in types. SECRET is the Gitleaks rule set (rule data
// converted to rules/gitleaks.json under its MIT licence); EMAIL, PHONE,
// CARD and IPV4 are personal data written here. Each type has a mode:
// off (not scanned), log (counted, text unchanged), mask (replaced with
// a placeholder, restored in the reply) or block (the request is
// refused). A placeholder is REDACTED_<TYPE>_<10 hex>, an HMAC of the
// value under a per-install key, so one value always maps to one
// placeholder and the model can reason about repeats without seeing
// them.
//
// The package never logs and never returns a detected value: findings
// carry the type, the rule and the placeholder only. It does not decide
// where text comes from or goes; the router (M2.3) calls it on every
// model request and reply.
package redact
