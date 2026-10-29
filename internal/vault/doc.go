// Package vault keeps serve's secrets, such as provider API keys,
// encrypted at rest with AES-256-GCM.
//
// It uses two keys. The vault key comes from DEVCULES_VAULT_KEY (or its
// _FILE), or from a key file serve generates on first start; it never
// touches the store. It wraps a random data key, which is kept wrapped
// in the store's meta repo and encrypts every secret. Rotating the vault
// key therefore rewraps one value in one write, and never re-encrypts
// the secrets themselves. Each secret is bound to its name as associated
// data, so a sealed value copied under another name fails to open.
//
// The vault never logs, and its errors name secrets, never their values.
// It lives only in serve: the runner and machine roles must never import
// it (arch_test.go enforces this transitively). It does not decide which
// secrets exist or who may read them; the router and providers do.
package vault
