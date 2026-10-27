// Package bus defines the event bus interface for Devcules and nothing else.
//
// A Bus carries transient events between parts of one process: every
// subscriber of a topic gets every message published after it
// subscribed, at most once, with no persistence and no replay. The exact
// delivery rules live in docs/bus.md and are enforced by the contract
// suite in internal/bus/bustest, which every implementation runs.
package bus
