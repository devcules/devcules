// Package machines owns the machine identity lifecycle (M4.1) and how those
// machines prove themselves on GET /link. Serve mints short-lived enrol codes;
// a machine redeems one once with its ed25519 public key to become enrolled.
// Thereafter it proves possession of the private key via a nonce signature
// without ever reusing the enrol code or the runner token pattern. The store
// holds only hashes and pubkeys; nonces are memory-only.
//
// It does no HTTP — internal/api exposes the code-issuing endpoint; cmd handles
// enrolling from the machine side.
package machines
