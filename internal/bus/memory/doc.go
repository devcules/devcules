// Package memory implements bus.Bus in process, on plain channels and a
// mutex, with the standard library only.
//
// Its job is fan-out with backpressure that never blocks the publisher:
// each subscriber drains a bounded buffer, and a full buffer drops the
// message for that subscriber only. It does not persist, replay, name
// topics, route between processes, count drops, or answer requests —
// those need different modules.
package memory
