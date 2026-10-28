// Package relay is the serve side of terminals: it routes browser
// streams to runner tmux sessions over the term.* link frames and
// back. Serve keeps no terminal state of its own — the list always
// comes from the runner, so a serve restart loses nothing, and resume
// is a fresh attach that makes tmux redraw.
//
// The relay owns request/reply correlation (by frame id, with a 5 s
// ceiling), per-browser backpressure queues, and browser stream
// lifecycle (slow browsers, runner offline, closed terminals). It runs
// hub handlers inline in the link read loop, so every handler only
// enqueues and returns; the browser pumps do the blocking work. It
// never imports internal/terminals/pty (the arch test forbids it) and
// never logs terminal input or output, which can hold secrets.
package relay
