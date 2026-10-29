// Package push sends Web Push notifications to the owner's browsers:
// the phone that answers approvals.
//
// It implements the three standards itself, on the standard library:
// RFC 8030 (the push service protocol: TTL, Urgency, 201 Created, 404
// and 410 for a dead subscription), RFC 8291 (aes128gcm message
// encryption to the browser's P-256 key and auth secret) and RFC 8292
// (VAPID: an ES256 JWT that identifies serve to the push service).
// The VAPID key pair is made on first start and kept in the vault as
// push/vapid_key; browsers subscribe with its public half.
//
// Subscriptions live in the store's PushSubscriptions repo, which only
// this package uses. A push service that answers 404 or 410 has
// dropped the subscription, so it is deleted.
//
// Messages are short and hold no secrets: a push travels through the
// browser vendor's service, encrypted but outside Devcules. Callers
// put the detail behind a link that needs a session. It does not
// queue or retry: a failed push is logged, and the approval still
// waits in the app.
package push
