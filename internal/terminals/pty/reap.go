package pty

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// reapGrace is how long reaped processes get after SIGTERM before
// SIGKILL.
const reapGrace = 2 * time.Second

// proc is one row of the process table.
type proc struct {
	pid, ppid, pgid int
}

var launchDirRE = regexp.MustCompile(`^launch-[0-9a-f]{16}$`)

// ValidLaunchDir reports an absolute agent launch dir path
// (…/launch-<16 hex>), the only kind Reap will sweep.
func ValidLaunchDir(dir string) bool {
	return filepath.IsAbs(dir) && filepath.Clean(dir) == dir && launchDirRE.MatchString(filepath.Base(dir))
}

// launchRoots is dir and, when it differs, its resolved path: macOS
// reports /private/var for a dir made under /var.
func launchRoots(dir string) []string {
	roots := []string{dir}
	if real, err := filepath.EvalSymlinks(dir); err == nil && real != dir {
		roots = append(roots, real)
	} else if !strings.HasPrefix(dir, "/private/") && (strings.HasPrefix(dir, "/var/") || strings.HasPrefix(dir, "/tmp/")) {
		// The dir may be gone already: name the macOS form anyway.
		roots = append(roots, "/private"+dir)
	}
	return roots
}

func within(roots []string, p string) bool {
	for _, r := range roots {
		if p == r || strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

// owned reports whether a process belongs to a launch: its
// executable, its working directory or any environment value (such as
// CODEX_HOME or CLAUDE_CONFIG_DIR) is the launch dir or inside it.
// Paths only, never process names.
func owned(roots []string, exe, cwd string, env []string) bool {
	if within(roots, exe) || within(roots, cwd) {
		return true
	}
	for _, kv := range env {
		if _, v, ok := strings.Cut(kv, "="); ok && within(roots, v) {
			return true
		}
	}
	return false
}

// ancestors is pid and every parent above it in table.
func ancestors(table []proc, pid int) map[int]bool {
	parent := map[int]int{}
	for _, p := range table {
		parent[p.pid] = p.ppid
	}
	out := map[int]bool{}
	for pid > 1 && !out[pid] {
		out[pid] = true
		pid = parent[pid]
	}
	return out
}

// Reap stops every process that belongs to launch dir (see owned),
// except the caller and its ancestors: an agent's launch script calls
// it through `devcules reap` while it still runs. It sends SIGTERM,
// waits up to reapGrace, then SIGKILLs those still matching (checked
// again, so a reused pid is spared). It returns the pids it signalled.
func Reap(dir string) ([]int, error) {
	if !ValidLaunchDir(dir) {
		return nil, errors.New("pty: not a launch dir")
	}
	table, err := listProcs()
	if err != nil {
		return nil, err
	}
	roots := launchRoots(dir)
	skip := ancestors(table, os.Getpid())
	match := func(pid int) bool {
		exe, cwd, env := procDetail(pid)
		return owned(roots, exe, cwd, env)
	}
	var hit []int
	for _, p := range table {
		if p.pid > 1 && !skip[p.pid] && match(p.pid) {
			hit = append(hit, p.pid)
		}
	}
	stop(hit, nil, match)
	return hit, nil
}

// tree is a process subtree captured before its tmux session dies:
// once the pane is gone its children are reparented and the links
// are lost.
type tree struct {
	pids   []int
	groups []int
}

// treeOf collects roots and all their descendants in table, and the
// process groups led by one of them (never a group led by anything
// else, so the runner's and tmux's own groups are safe).
func treeOf(table []proc, roots []int) tree {
	children := map[int][]int{}
	for _, p := range table {
		children[p.ppid] = append(children[p.ppid], p.pid)
	}
	in := map[int]bool{}
	queue := slices.Clone(roots)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if pid <= 1 || in[pid] {
			continue
		}
		in[pid] = true
		queue = append(queue, children[pid]...)
	}
	var t tree
	self := os.Getpid()
	for _, p := range table {
		if !in[p.pid] || p.pid == self {
			continue
		}
		t.pids = append(t.pids, p.pid)
		if p.pgid == p.pid {
			t.groups = append(t.groups, p.pgid)
		}
	}
	return t
}
