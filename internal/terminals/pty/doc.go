// Package pty is the runner side of terminals: it drives one private
// tmux server and the attach clients browsers watch through. tmux is
// the source of truth — sessions, scrollback and redraws all live
// there, so resume needs no buffer of ours: a new attach makes tmux
// redraw the screen.
//
// The manager owns processes (open/attach/resize/detach/close/list);
// link.go wires those to the term.* link frames with per-stream
// output batching. The relay (serve side) never imports this package;
// the arch test enforces that. tmux runs as the runner's own uid (see
// docs/terminals.md for what that implies for /link/runner.token).
package pty
