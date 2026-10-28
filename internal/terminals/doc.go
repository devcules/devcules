// Package terminals defines the terminal frame payloads shared by the
// runner side (pty, driving tmux) and the serve side (relay, routing
// browser streams). It owns validation: ids, sizes and base64 shapes
// are checked here, so both ends reject the same bad frames the same
// way. It does no I/O: pty owns processes, relay owns the link and
// the browser sockets.
package terminals
