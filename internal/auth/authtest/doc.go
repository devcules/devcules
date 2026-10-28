// Package authtest is a test-only software WebAuthn authenticator.
// It makes P-256 credentials with "none" attestation and signs
// assertions, so auth's ceremonies meet a real (if naive) peer
// without a browser. The knobs (counter, flags, origin and RP id
// overrides) drive the negative ceremony tests.
//
// It does not verify anything: that is the library's job, and these
// responses exist to exercise it.
package authtest
