package pty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"

	"github.com/devcules/devcules/internal/terminals"
)

// tmuxConf is written by the manager and passed to every tmux call, so
// the private server always starts with these: no status line stealing
// rows, deep scrollback for resume, no escape delay on Meta keys, a
// 256-color terminfo the shells understand, and windows that follow
// the latest attached client (phone and desktop share one terminal).
const tmuxConf = `set -g status off
set -g history-limit 10000
set -g escape-time 0
set -g default-terminal tmux-256color
set -g window-size latest
`

// defaultSize is a session's window until the first attach resizes it.
const (
	defaultCols = 80
	defaultRows = 24
)

// tmuxTimeout bounds one tmux call: a wedged server must fail the
// request, never the handler goroutine.
const tmuxTimeout = 10 * time.Second

// Manager owns tmux terminals under one private server. The zero
// Manager is not usable; build one with New. It is safe for concurrent
// use; tmux itself is the session source of truth, while attach
// clients are tracked here by stream id.
type Manager struct {
	socket    string // TMUX_TMPDIR: the private server's socket dir
	workspace string // start directory for new sessions
	conf      string // written tmux.conf path
	max       int    // terminal cap (terminals.MaxTerminals in production)
	log       *slog.Logger

	// openMu makes the cap check and new-session one step, so two
	// concurrent opens cannot both pass at 15 terminals.
	openMu sync.Mutex

	mu       sync.Mutex
	attaches map[string]*attach
}

// inputQueue bounds the keystroke frames waiting for one PTY. Writes
// happen on the attach's own goroutine, so a PTY that stops reading
// never blocks the link read loop; a full queue ends the attach (the
// browser reattaches and tmux redraws).
const inputQueue = 256

// attach is one browser's view: a tmux attach client inside a PTY.
type attach struct {
	term   string
	stream string
	file   *os.File // pty master; closing it ends the client
	cmd    *exec.Cmd

	in        chan []byte   // queued browser input, drained by writer
	done      chan struct{} // closed once the attach is shut
	closeOnce sync.Once
	waitOnce  sync.Once
}

// newAttach wraps a started client and starts its input writer.
func newAttach(term, stream string, file *os.File, cmd *exec.Cmd) *attach {
	a := &attach{term: term, stream: stream, file: file, cmd: cmd,
		in: make(chan []byte, inputQueue), done: make(chan struct{})}
	go a.writer()
	return a
}

// writer feeds queued input to the PTY, looping short writes, until
// the attach shuts or a write fails.
func (a *attach) writer() {
	for {
		select {
		case <-a.done:
			return
		case b := <-a.in:
			for len(b) > 0 {
				n, err := a.file.Write(b)
				if err != nil {
					return
				}
				b = b[n:]
			}
		}
	}
}

// close shuts the attach once: the writer stops and the tmux client
// gets SIGHUP, as if its terminal hung up. Closing the PTY master alone
// is not enough: the master is a blocking file, so Close waits for the
// pump's in-flight Read, which only returns once the client exits.
// Safe from any goroutine.
func (a *attach) close() {
	a.closeOnce.Do(func() {
		close(a.done)
		if a.cmd != nil && a.cmd.Process != nil {
			_ = a.cmd.Process.Signal(syscall.SIGHUP)
		}
		_ = a.file.Close()
	})
}

// wait reaps the client process once. exec.Cmd.Wait must not run
// twice or concurrently, and both the pump and the closers reap.
func (a *attach) wait() {
	a.waitOnce.Do(func() {
		if a.cmd != nil {
			_ = a.cmd.Wait()
		}
	})
}

// HasTmux reports whether tmux is installed. Tests skip the real-tmux
// suite without it (and fail when CI=true); the runner logs a warning
// and links without terminals.
func HasTmux() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// NewForTest returns a Manager on a private socket dir for tests that
// never touch the default tmux server. Validation and bookkeeping run
// before any process spawns; tmux-backed behavior lives in
// tmux_test.go (skip-gated), while link_test.go drives the error paths
// through a live link.
func NewForTest(socketDir string) (*Manager, error) {
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		return nil, err
	}
	conf := filepath.Join(socketDir, "tmux.conf")
	if err := os.WriteFile(conf, []byte(tmuxConf), 0o600); err != nil {
		return nil, err
	}
	return &Manager{
		socket:    socketDir,
		workspace: socketDir,
		conf:      conf,
		max:       2,
		log:       slog.New(slog.DiscardHandler),
		attaches:  map[string]*attach{},
	}, nil
}

// New returns a Manager with a private socket dir and tmux config. It
// fails fast when tmux is missing, with next steps: the runner logs a
// warning and links without terminals rather than dying.
func New(socketDir, workspace string, log *slog.Logger) (*Manager, error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, fmt.Errorf("tmux not found: install tmux for terminal support")
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := os.MkdirAll(socketDir, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create tmux socket dir %s: %s", socketDir, err)
	}
	conf := filepath.Join(socketDir, "tmux.conf")
	if err := os.WriteFile(conf, []byte(tmuxConf), 0o600); err != nil {
		return nil, fmt.Errorf("cannot write tmux config: %s", err)
	}
	return &Manager{
		socket:    socketDir,
		workspace: workspace,
		conf:      conf,
		max:       terminals.MaxTerminals,
		log:       log,
		attaches:  map[string]*attach{},
	}, nil
}

// env scrubs the environment for tmux and shells down to five safe
// variables plus the socket dir. It is built from scratch — never
// from os.Environ — so no DEVCULES_* variable can ever reach a shell.
func (m *Manager) env() []string {
	home := os.Getenv("HOME")
	if home == "" {
		home = "/home/devcules"
	}
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/bin:/usr/bin:/bin"
	}
	return []string{
		"TERM=xterm-256color",
		"HOME=" + home,
		"PATH=" + path,
		"LANG=C.UTF-8",
		"SHELL=/bin/bash",
		"TMUX_TMPDIR=" + m.socket,
	}
}

// tmux builds a tmux call with argument lists, never through a shell.
// Every call names the private server (-L) and config (-f).
func (m *Manager) tmux(ctx context.Context, args ...string) *exec.Cmd {
	argv := append([]string{"-L", "devcules", "-f", m.conf}, args...)
	cmd := exec.CommandContext(ctx, "tmux", argv...)
	cmd.Env = m.env()
	return cmd
}

// run executes one tmux call and returns trimmed combined output. An
// execution error names the subcommand and the fix, never the raw
// output (which can hold paths); callers needing output inspect it
// themselves.
func (m *Manager) run(ctx context.Context, args ...string) (string, error) {
	ctx, stop := context.WithTimeout(ctx, tmuxTimeout)
	defer stop()
	out, err := m.tmux(ctx, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("tmux %s failed: %s", args[0], shortErr(err))
	}
	return strings.TrimSpace(string(out)), nil
}

// shortErr keeps exec failures to one line for fixed reason strings.
func shortErr(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

// Open creates the detached tmux session for id in the workspace with
// a login bash. It returns the creation time for the list.
func (m *Manager) Open(ctx context.Context, id string) (time.Time, error) {
	if !terminals.ValidID(id) {
		return time.Time{}, fmt.Errorf("term: want 16 hex chars")
	}
	m.openMu.Lock()
	defer m.openMu.Unlock()
	terms, err := m.List(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if len(terms) >= m.max {
		return time.Time{}, fmt.Errorf("too many terminals: at most %d per runner", m.max)
	}
	name := terminals.SessionName(id)
	if _, err := m.run(ctx, "new-session", "-d", "-s", name, "-c", m.workspace,
		"-x", strconv.Itoa(defaultCols), "-y", strconv.Itoa(defaultRows), "bash", "-l"); err != nil {
		if strings.Contains(err.Error(), "duplicate session") {
			return time.Time{}, fmt.Errorf("terminal exists")
		}
		return time.Time{}, fmt.Errorf("tmux failed to open the terminal")
	}
	return time.Now(), nil
}

// hasSession reports whether the tmux session exists.
func (m *Manager) hasSession(ctx context.Context, id string) bool {
	_, err := m.run(ctx, "has-session", "-t", terminals.SessionName(id))
	return err == nil
}

// Attach starts an attach client for one browser stream in a PTY sized
// cols×rows. Each browser gets its own client, so phone and desktop
// share one terminal.
func (m *Manager) Attach(ctx context.Context, id, stream string, cols, rows int) (*attach, error) {
	if !terminals.ValidID(id) {
		return nil, fmt.Errorf("term: want 16 hex chars")
	}
	if !terminals.ValidID(stream) {
		return nil, fmt.Errorf("stream: want 16 hex chars")
	}
	if !terminals.ValidSize(cols, rows) {
		return nil, fmt.Errorf("size: want cols %d-%d, rows %d-%d",
			terminals.MinCols, terminals.MaxCols, terminals.MinRows, terminals.MaxRows)
	}
	m.mu.Lock()
	_, dup := m.attaches[stream]
	m.mu.Unlock()
	if dup {
		return nil, fmt.Errorf("stream exists")
	}
	if !m.hasSession(ctx, id) {
		return nil, fmt.Errorf("unknown terminal")
	}
	size := &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
	// ctx is the link's: when the link drops, the client dies with it.
	cmd := m.tmux(ctx, "attach", "-t", terminals.SessionName(id))
	file, err := pty.StartWithSize(cmd, size)
	if err != nil {
		return nil, fmt.Errorf("tmux failed to attach")
	}
	a := newAttach(id, stream, file, cmd)
	m.mu.Lock()
	if _, dup := m.attaches[stream]; dup {
		m.mu.Unlock()
		a.close()
		a.wait()
		return nil, fmt.Errorf("stream exists")
	}
	m.attaches[stream] = a
	m.mu.Unlock()
	return a, nil
}

// Read reads attach-client output: the test helper for tmux_test (the
// pump reads directly in production). Unknown streams fail instead of
// blocking forever.
func (m *Manager) Read(stream string, b []byte) (int, error) {
	m.mu.Lock()
	a, ok := m.attaches[stream]
	m.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("unknown stream")
	}
	return a.file.Read(b)
}

// Write queues browser input for the attach client's PTY and never
// blocks: it runs inline in the link read loop. A full queue means the
// client stopped reading, so the attach ends (its pump reports
// term.exit) and the browser reattaches.
func (m *Manager) Write(stream string, b []byte) error {
	m.mu.Lock()
	a, ok := m.attaches[stream]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown stream")
	}
	select {
	case a.in <- b:
		return nil
	case <-a.done:
		return fmt.Errorf("unknown stream")
	default:
		m.forget(stream)
		return fmt.Errorf("terminal input queue full")
	}
}

// Resize forwards a browser window change: the PTY resize signals the
// attach client, and tmux (window-size latest) follows it.
func (m *Manager) Resize(ctx context.Context, stream string, cols, rows int) error {
	if !terminals.ValidSize(cols, rows) {
		return fmt.Errorf("size: want cols %d-%d, rows %d-%d",
			terminals.MinCols, terminals.MaxCols, terminals.MinRows, terminals.MaxRows)
	}
	m.mu.Lock()
	a, ok := m.attaches[stream]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown stream")
	}
	if err := pty.Setsize(a.file, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		return fmt.Errorf("terminal resize failed")
	}
	return nil
}

// Detach ends one attach client by closing its PTY; the tmux session
// keeps running for the next attach. The pump observes EOF and reports
// term.exit, so Detach only closes and forgets.
func (m *Manager) Detach(stream string) {
	m.mu.Lock()
	a, ok := m.attaches[stream]
	if ok {
		delete(m.attaches, stream)
	}
	m.mu.Unlock()
	if ok {
		a.close()
		a.wait()
	}
}

// forget removes one attach from the map and closes its PTY master,
// for pumps that ended on their own (EOF, link death). Closing an
// already-detached master fails harmlessly; every path here tolerates
// the double.
func (m *Manager) forget(stream string) {
	m.mu.Lock()
	a, ok := m.attaches[stream]
	if ok {
		delete(m.attaches, stream)
	}
	m.mu.Unlock()
	if ok {
		a.close()
	}
}

// dropStreams ends every attach client without touching tmux sessions:
// serve forgets its streams on unlink, so stale clients must not keep
// pumping into a dead link. Sessions keep running for reattach.
func (m *Manager) dropStreams() {
	m.mu.Lock()
	all := make([]*attach, 0, len(m.attaches))
	for stream, a := range m.attaches {
		delete(m.attaches, stream)
		all = append(all, a)
	}
	m.mu.Unlock()
	for _, a := range all {
		a.close()
		a.wait()
	}
}

// Close kills the tmux session and every attach on it. Closing a
// missing session succeeds: deletes are idempotent, and the relay
// answers 204 either way.
func (m *Manager) Close(ctx context.Context, id string) error {
	if !terminals.ValidID(id) {
		return fmt.Errorf("term: want 16 hex chars")
	}
	m.mu.Lock()
	var dying []*attach
	for stream, a := range m.attaches {
		if a.term == id {
			delete(m.attaches, stream)
			dying = append(dying, a)
		}
	}
	m.mu.Unlock()
	for _, a := range dying {
		a.close()
		a.wait()
	}
	_, err := m.run(ctx, "kill-session", "-t", terminals.SessionName(id))
	// An agent's launch dir holds its token: gone with the terminal.
	_ = os.RemoveAll(m.LaunchDir(id))
	if err != nil {
		if !m.hasSession(ctx, id) {
			return nil
		}
		return fmt.Errorf("tmux failed to close the terminal")
	}
	return nil
}

// List returns the live tmux sessions as terminal infos. A missing
// server means no terminals, not an error: the runner may have no
// sessions yet.
func (m *Manager) List(ctx context.Context) ([]terminals.TermInfo, error) {
	ctx, stop := context.WithTimeout(ctx, tmuxTimeout)
	defer stop()
	out, err := m.tmux(ctx, "list-sessions", "-F", "#{session_name} #{session_created} #{"+agentOption+"}").CombinedOutput()
	if err != nil {
		text := string(out)
		if strings.Contains(text, "no server running") ||
			strings.Contains(text, "No such file or directory") ||
			strings.Contains(text, "failed to connect") {
			return nil, nil
		}
		return nil, fmt.Errorf("tmux failed to list terminals")
	}
	var terms []terminals.TermInfo
	for line := range strings.Lines(strings.TrimSpace(string(out))) {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name, created := fields[0], fields[1]
		id, found := strings.CutPrefix(name, "dvc-")
		if !found || !terminals.ValidID(id) {
			continue
		}
		secs, err := strconv.ParseInt(strings.TrimSpace(created), 10, 64)
		if err != nil {
			continue
		}
		info := terminals.TermInfo{Term: id, Created: secs}
		if len(fields) > 2 && terminals.ValidAgentID(fields[2]) {
			info.Agent = fields[2]
		}
		terms = append(terms, info)
	}
	return terms, nil
}

// reap waits out a finished attach client so no zombie lingers. The
// pump calls it after the PTY hits EOF; closers may already have
// waited (ignored), and pipe-backed test attaches have no process.
func reap(a *attach) {
	a.wait()
}

// Shutdown kills the private tmux server and every session on it.
// Tests call it so no tmux server outlives its test; the runner never
// does, because sessions must survive serve restarts.
func (m *Manager) Shutdown(ctx context.Context) {
	m.dropStreams()
	_, _ = m.run(ctx, "kill-server")
}

// eofLike reports a PTY read end: EOF, or EIO when the slave side
// went away first (the standard Linux PTY goodbye).
func eofLike(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO)
}
