package pty

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/devcules/devcules/internal/terminals"
)

// Browse lists the subdirectories of dir for the project folder picker:
// absolute, or relative to the workspace, and empty for the workspace
// itself. It reads names only (never file contents) and marks the ones
// holding .git, so the picker can say which folders are repositories.
// A shell on this runner can read the same, so the listing reveals
// nothing the owner could not already see.
func (m *Manager) Browse(dir string) (terminals.Browsed, error) {
	path := m.workspace
	if dir != "" {
		if !terminals.ValidDir(dir) {
			return terminals.Browsed{}, errors.New("bad directory")
		}
		path = dir
		if !filepath.IsAbs(path) {
			path = filepath.Join(m.workspace, path)
		}
	}
	path = filepath.Clean(path)
	entries, err := os.ReadDir(path)
	if err != nil {
		return terminals.Browsed{}, errors.New("directory not found")
	}
	out := terminals.Browsed{Path: path, Workspace: m.workspace, Git: isRepo(path), Remote: remoteOf(path), Entries: []terminals.DirEntry{}}
	if parent := filepath.Dir(path); parent != path {
		out.Parent = parent
	}
	if home, err := os.UserHomeDir(); err == nil {
		out.Home = home
	}
	for _, e := range entries {
		if !isDir(path, e) {
			continue
		}
		if len(out.Entries) == terminals.MaxDirEntries {
			out.Truncated = true
			break
		}
		name := e.Name()
		sub := filepath.Join(path, name)
		out.Entries = append(out.Entries, terminals.DirEntry{
			Name:   name,
			Git:    isRepo(sub),
			Hidden: strings.HasPrefix(name, "."),
			Remote: remoteOf(sub),
		})
	}
	sort.Slice(out.Entries, func(i, j int) bool {
		return strings.ToLower(out.Entries[i].Name) < strings.ToLower(out.Entries[j].Name)
	})
	return out, nil
}

// isDir follows symlinks, so a linked checkout lists like a folder.
func isDir(parent string, e os.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink == 0 {
		return false
	}
	st, err := os.Stat(filepath.Join(parent, e.Name()))
	return err == nil && st.IsDir()
}

// isRepo reports a git working tree: a .git directory, or the .git
// file a worktree or submodule carries.
func isRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// remoteOf reads origin's URL and classifies its host (github, gitlab,
// azure). A repo without an origin, or on a host we don't recognise,
// stays empty. The child is bounded: browse runs inside the link's
// request loop, so a hung git (no config, an interactive prompt, a
// slow credential helper) is dropped after a second rather than
// block every terminal on the link. Empty is the right answer for
// anything that is not a one-shot get-url success.
func remoteOf(dir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin")
	cmd.Stdin = nil // never inherit: a credential helper may not hold the link
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return terminals.DetectRemote(strings.TrimSpace(string(out)))
}
