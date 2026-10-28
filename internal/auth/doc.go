// Package auth owns password sign-in: admin bootstrap, argon2id
// passwords, cookie sessions, and sign-in rate limiting.
//
// It does not do HTTP (internal/api maps its errors to statuses), it
// does not implement storage (it uses the store's Users and Sessions
// repos), and it does not do passkeys, API tokens, or audit events:
// those are later modules and are not stubbed here.
package auth
