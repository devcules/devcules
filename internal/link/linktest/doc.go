// Package linktest runs a real HTTP server and real WebSocket peers
// without touching the loopback network. The sandbox (and some CI
// runners) cannot bind local ports, so httptest.NewServer is not an
// option here: instead a fake net.Listener hands out net.Pipe pairs,
// an http.Client dials the client end, and github.com/coder/websocket
// performs a genuine HTTP upgrade and genuine frames over the pipe.
//
// Tests in internal/link/hub and internal/link/dial build their
// servers with Serve and dial them with the returned client, so every
// handshake, auth check, close code and heartbeat runs the real code
// paths, not stubs.
package linktest
