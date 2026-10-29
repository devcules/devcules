// Package audit keeps Devcules' append-only audit log: who did what,
// when, from where. Sign-ins and sign-outs, passkey changes, terminal
// opens and closes, redaction policy changes, launch token mints and
// revokes, trace reads and audit exports all land here.
//
// Append-only is enforced three times over. The store's audit repo
// has no update or delete; the SQLite table refuses both with
// triggers; and every entry carries a SHA-256 hash chained to the one
// before, so an edit made around the database (a hand-edited file, a
// restored backup with rows removed) shows up in Verify and in serve's
// startup check.
//
// Entries hold names, ids and short labels only: never a password, a
// token, a key or a redacted value. Export writes the log as JSON
// lines, hashes included, so an outside tool can check the chain.
//
// The package does not decide what to audit (callers do), does not
// ship entries anywhere (export does), and does not prune: the log
// grows for the life of the install.
package audit
