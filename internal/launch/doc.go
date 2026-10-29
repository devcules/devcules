// Package launch issues per-launch tokens: the only credential an
// agent ever holds for a model. A token works only against serve's
// model router, expires (12 hours by default, 7 days at most), can be
// revoked, and can be limited to one provider. Real provider keys stay
// in the vault and never leave serve.
//
// Tokens are "dvl_" plus 32 random bytes in base64url. Only their
// SHA-256 reaches the store; the token is shown once, when minted.
// The package does not launch agents (M3.3 does, minting a token per
// launch) and does not route model calls (internal/router checks
// tokens through Check).
package launch
