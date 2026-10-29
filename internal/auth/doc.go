// Package auth owns sign-in: admin bootstrap, argon2id passwords,
// cookie sessions, sign-in rate limiting, and WebAuthn passkeys.
//
// It does not do HTTP (internal/api maps its errors to statuses), it
// does not implement storage (it uses the store's Users, Sessions
// and Passkeys repos), and it does not do API tokens or audit events:
// those are later modules and are not stubbed here.
package auth
