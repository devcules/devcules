package memory

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/devcules/devcules/internal/bus"
)

// bufferSize bounds each subscriber queue. Publish never blocks: beyond
// this many unread messages, extras drop for that subscriber only. The
// contract documents the bound so no module assumes lossless delivery.
const bufferSize = 64

// subscriber is one subscription: an entry in the topic set plus its queue.
// The queue is closed exactly once, either by unsubscribe or by Close,
// always while holding the bus mutex, so Publish can never send on a
// closed channel.
type subscriber struct {
	ch chan []byte
}

// memoryBus is a bus.Bus for one process.
type memoryBus struct {
	mu     sync.Mutex
	closed bool
	done   chan struct{}
	subs   map[string]map[*subscriber]struct{}
	active atomic.Int64
}

// New returns an empty bus.
func New() bus.Bus {
	return &memoryBus{
		done: make(chan struct{}),
		subs: make(map[string]map[*subscriber]struct{}),
	}
}

// watchers reports live subscription goroutines. Tests use it to prove
// none outlive their subscription; production code never reads it.
func (b *memoryBus) watchers() int {
	return int(b.active.Load())
}

// Publish delivers a copy of msg to every current subscriber of topic, in
// order per subscriber, dropping for a subscriber whose buffer is full.
func (b *memoryBus) Publish(ctx context.Context, topic string, msg []byte) error {
	if topic == "" {
		return fmt.Errorf("bus: publish: %w", bus.ErrBadTopic)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return fmt.Errorf("bus: publish: %w", bus.ErrClosed)
	}
	for sub := range b.subs[topic] {
		select {
		case sub.ch <- bytes.Clone(msg):
		default:
		}
	}
	return nil
}

// Subscribe registers a subscriber for topic. Cancelling ctx removes it
// and closes its channel; the waiter goroutine always exits with the
// subscription, so none outlive it.
func (b *memoryBus) Subscribe(ctx context.Context, topic string) (<-chan []byte, error) {
	if topic == "" {
		return nil, fmt.Errorf("bus: subscribe: %w", bus.ErrBadTopic)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, fmt.Errorf("bus: subscribe: %w", bus.ErrClosed)
	}
	sub := &subscriber{ch: make(chan []byte, bufferSize)}
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[*subscriber]struct{})
	}
	b.subs[topic][sub] = struct{}{}
	b.active.Add(1)
	b.mu.Unlock()
	go b.watch(ctx, topic, sub)
	return sub.ch, nil
}

// watch ends the subscription whichever finishes first: callers'
// cancellation or bus Close. Both paths hold the mutex against Publish
// and against each other, so the channel closes exactly once.
func (b *memoryBus) watch(ctx context.Context, topic string, sub *subscriber) {
	defer b.active.Add(-1)
	select {
	case <-ctx.Done():
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.closed {
			return
		}
		delete(b.subs[topic], sub)
		if len(b.subs[topic]) == 0 {
			delete(b.subs, topic)
		}
		close(sub.ch)
	case <-b.done:
	}
}

// Close disconnects: every subscriber channel closes, later Publish and
// Subscribe fail with ErrClosed, and watchers exit. Repeating Close is a
// no-op, so deferred Closes stay safe.
func (b *memoryBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for _, subs := range b.subs {
		for sub := range subs {
			close(sub.ch)
		}
	}
	b.subs = make(map[string]map[*subscriber]struct{})
	close(b.done)
	return nil
}
