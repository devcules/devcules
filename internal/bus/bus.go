package bus

import (
	"context"
	"errors"
)

// Delivery errors. Implementations wrap them with %w (never %s) so
// callers match with errors.Is without parsing messages.
var (
	// ErrClosed reports use after Close.
	ErrClosed = errors.New("bus: closed")
	// ErrBadTopic reports an empty topic. Topics match exactly; there
	// are no wildcards.
	ErrBadTopic = errors.New("bus: bad topic")
)

// Bus is a transient, at-most-once event bus. Later modules may add
// backends (Postgres LISTEN/NOTIFY is a stretch goal); the interface
// stays this small. See docs/bus.md for the delivery rules every
// implementation honors.
type Bus interface {
	// Publish delivers msg to every current subscriber of topic, in
	// publish order per subscriber. It never blocks on a slow
	// subscriber: a full buffer drops the message for that subscriber
	// only. msg is copied, so the caller may reuse its buffer.
	Publish(ctx context.Context, topic string, msg []byte) error
	// Subscribe returns the subscriber channel for topic. Cancelling
	// ctx unsubscribes and closes the channel.
	Subscribe(ctx context.Context, topic string) (<-chan []byte, error)
	// Close disconnects: it closes every subscriber channel, after
	// which Publish and Subscribe return ErrClosed. It is idempotent.
	Close() error
}
