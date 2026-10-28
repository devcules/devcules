package linktest

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// pipeAddr identifies pipe connections. The HTTP transport ignores the
// address (every dial yields an anonymous pipe), and the WebSocket
// handshake never inspects it either.
type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// pipeListener is a net.Listener over net.Pipe. Accept blocks until
// DialContext creates the next pair, so server and client rendezvous
// without any socket. It remembers every server end it handed out:
// http.Server.Close never touches hijacked connections (WebSockets),
// so Close kills them here, the way a dead process drops its sockets.
type pipeListener struct {
	pipes chan net.Conn
	done  chan struct{}
	once  sync.Once

	mu    sync.Mutex
	conns []net.Conn
}

func newPipeListener() *pipeListener {
	return &pipeListener{pipes: make(chan net.Conn), done: make(chan struct{})}
}

// dial creates a pair, hands the server end to a blocked Accept, and
// returns the client end. It fails only after Close.
func (l *pipeListener) dial() (net.Conn, error) {
	client, server := net.Pipe()
	l.mu.Lock()
	l.conns = append(l.conns, server)
	l.mu.Unlock()
	select {
	case l.pipes <- server:
		return client, nil
	case <-l.done:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.pipes:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() {
		close(l.done)
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, c := range l.conns {
			_ = c.Close()
		}
		l.conns = nil
	})
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

// Server is a real http.Server on a pipe listener. The client dials
// whatever listener is current, so Restart swaps the serving stack
// underneath a reconnecting dialer — the in-process version of
// restarting serve while the runner keeps backing off.
type Server struct {
	t        *testing.T
	mu       sync.Mutex
	ln       *pipeListener
	srv      *http.Server
	client   *http.Client
	serveErr chan error
}

// Serve starts handler on a pipe listener and returns a Server whose
// Client dials it. Both http://pipe/ URLs and ws://pipe/ URLs route
// to the same stack; only the path distinguishes them.
func Serve(t *testing.T, handler http.Handler) *Server {
	t.Helper()
	s := &Server{t: t, serveErr: make(chan error, 8)}
	s.client = &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				s.mu.Lock()
				ln := s.ln
				s.mu.Unlock()
				if ln == nil {
					return nil, net.ErrClosed
				}
				return ln.dial()
			},
		},
	}
	s.start(handler)
	t.Cleanup(s.Close)
	return s
}

func (s *Server) start(handler http.Handler) {
	ln := newPipeListener()
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	s.mu.Lock()
	s.ln = ln
	s.srv = srv
	s.mu.Unlock()
	// The goroutine closes over locals, never the struct fields: a
	// later Restart or Close must not race the serve loop startup.
	go func() { s.serveErr <- srv.Serve(ln) }()
}

// Restart drops every connection (like a serve restart: the process is
// gone, so in-flight links die) and serves handler on a fresh listener.
// The client keeps dialing the current listener, so a dialer with
// backoff reconnects without any test-side rewiring.
func (s *Server) Restart(handler http.Handler) {
	s.mu.Lock()
	old := s.srv
	oldLn := s.ln
	s.mu.Unlock()
	// Close aborts in-flight requests; the listener's Close kills the
	// hijacked links, which the server never tracks. Shutdown would
	// wait politely instead.
	_ = old.Close()
	_ = oldLn.Close()
	// Drain the old server's terminal error so a restart never blocks
	// a later Close waiting on a full channel.
	select {
	case <-s.serveErr:
	default:
	}
	s.start(handler)
}

// Client returns the HTTP client that dials this server. WebSocket
// dialers take it as DialOptions.HTTPClient.
func (s *Server) Client() *http.Client { return s.client }

// Close stops the server and the listener. In-flight connections die
// with it, mirroring process exit.
func (s *Server) Close() {
	s.mu.Lock()
	srv, ln := s.srv, s.ln
	s.srv, s.ln = nil, nil
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
	if ln != nil {
		_ = ln.Close()
	}
	// Drain terminal serve errors without blocking: every start
	// enqueues exactly one, and a blocked sender would wedge Close.
	for {
		select {
		case <-s.serveErr:
		default:
			return
		}
	}
}
