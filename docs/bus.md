# Event bus

`internal/bus` defines the interface; `internal/bus/memory` implements
it in process; `internal/bus/bustest` holds the shared contract suite
every backend runs. The Postgres `LISTEN/NOTIFY` backend is a stretch
goal; the interface stays small when it lands.

## Delivery rules

- **At-most-once, no persistence, no replay.** A subscriber sees only
  messages published after its `Subscribe` returned.
- **Fan-out.** Every subscriber of a topic gets every message. Topics
  match exactly; there are no wildcards. An empty topic is `ErrBadTopic`.
  An empty message is allowed.
- **Order.** Messages from one publisher on one topic arrive in publish
  order. Across publishers there is no order.
- **Never blocks.** `Publish` never waits on a slow subscriber. Each
  subscriber has a bounded buffer (64); when it is full the message
  drops for that subscriber only. This is the contract, not a tuning
  knob: no module may assume lossless delivery. (Lossless needs a
  different module.)
- **Copies both ways.** `Publish` clones the message per subscriber, so
  the caller may reuse its buffer and subscribers cannot corrupt each
  other.
- **Cancellation unsubscribes.** Cancelling the `Subscribe` context
  removes the subscription and closes its channel.
- **Close ends everything.** `Close` closes every subscriber channel;
  `Publish` and `Subscribe` afterwards return `ErrClosed`. Repeating
  `Close` is a no-op. No goroutine outlives its subscription.
- **Cancelled contexts fail fast.** `Publish` and `Subscribe` with a
  cancelled context return an error matching `context.Canceled`.
  An empty topic is checked first.
