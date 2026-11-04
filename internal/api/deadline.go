package api

import (
	"net/http"
	"time"
)

// Long handlers vs serve's timeouts. cmd/devcules/serve.go runs the
// http.Server with ReadTimeout and WriteTimeout of 10 s, which suits
// every quick JSON route. A handler whose work can take longer (a chat
// turn of many model calls and approval waits, an agent CLI install, a
// run of slow ICS feeds) would lose its reply: the write deadline is
// armed when the request is read, so the final write fails and the
// client sees EOF. (net/http clears the read deadline for its
// background read, so the read side does not cut the work.)
//
// These handlers write once, after the work. A write deadline during
// the work frees nothing (the work is bounded by its own timeouts and
// by r.Context(), which ends when the client goes away); it only drops
// the answer. So liftWrite clears the deadline for the work and re-arms
// it, now + answerWindow, at the handler's first write: the answer
// keeps serve's slow-reader protection.

// answerWindow is the time a handler gets to write its answer once its
// work is done. It matches serve's WriteTimeout.
const answerWindow = 10 * time.Second

// liftWrite clears serve's write deadline for one long request and
// returns the ResponseWriter the handler must answer through: its
// first WriteHeader or Write sets the deadline to now + answerWindow.
// Call it after the request body is read (the body keeps serve's read
// deadline), as `w = liftWrite(w)`, and write only through the result.
// Deadline errors are ignored: a ResponseWriter that cannot set
// deadlines (a test recorder) has none to lift.
//
// Not for streaming handlers (SSE, chunked progress, websocket
// upgrades): the deadline is armed once at the first write and is
// absolute, so a stream would be cut answerWindow after it starts.
// Flush passes through, and NewResponseController reaches Hijack and
// the rest through Unwrap, but a plain w.(http.Hijacker) assertion on
// the result fails.
func liftWrite(w http.ResponseWriter) http.ResponseWriter {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	return &armedWriter{ResponseWriter: w}
}

// armedWriter re-arms the write deadline just before the answer goes
// out. Unwrap keeps http.NewResponseController working through it.
type armedWriter struct {
	http.ResponseWriter
	armed bool
}

func (a *armedWriter) arm() {
	if !a.armed {
		a.armed = true
		_ = http.NewResponseController(a.ResponseWriter).SetWriteDeadline(time.Now().Add(answerWindow))
	}
}

func (a *armedWriter) WriteHeader(code int) {
	a.arm()
	a.ResponseWriter.WriteHeader(code)
}

func (a *armedWriter) Write(b []byte) (int, error) {
	a.arm()
	return a.ResponseWriter.Write(b)
}

// Flush lets a plain w.(http.Flusher) assertion work through the
// wrapper. It arms first, since a flush writes the header.
func (a *armedWriter) Flush() {
	a.arm()
	_ = http.NewResponseController(a.ResponseWriter).Flush()
}

func (a *armedWriter) Unwrap() http.ResponseWriter { return a.ResponseWriter }
