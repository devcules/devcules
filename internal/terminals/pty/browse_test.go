package pty

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devcules/devcules/internal/terminals"
)

func TestBrowse(t *testing.T) {
	ws := t.TempDir()
	for _, d := range []string{"shop/.git", "api", ".cache", "Zeta"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(ws, "api"), filepath.Join(ws, "api-link")); err != nil {
		t.Fatal(err)
	}
	m := &Manager{workspace: ws}

	got, err := m.Browse("")
	if err != nil {
		t.Fatalf("Browse: %s", err)
	}
	if got.Path != ws || got.Workspace != ws || got.Parent != filepath.Dir(ws) || got.Git {
		t.Fatalf("listing = %+v", got)
	}
	want := []terminals.DirEntry{{Name: ".cache", Hidden: true}, {Name: "api"}, {Name: "api-link"}, {Name: "shop", Git: true}, {Name: "Zeta"}}
	if len(got.Entries) != len(want) {
		t.Fatalf("entries = %+v, want %+v (files skipped, links followed)", got.Entries, want)
	}
	for i := range want {
		if got.Entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, got.Entries[i], want[i])
		}
	}
	inside, err := m.Browse("shop")
	if err != nil || inside.Path != filepath.Join(ws, "shop") || !inside.Git {
		t.Fatalf("relative repo = %+v (%v)", inside, err)
	}
	if _, err := m.Browse("missing"); err == nil || err.Error() != "directory not found" {
		t.Fatalf("missing = %v", err)
	}
	if _, err := m.Browse("../etc"); err == nil {
		t.Fatal("an escaping path listed")
	}
	if root, err := m.Browse("/"); err != nil || root.Parent != "" {
		t.Fatalf("root = %+v (%v), want no parent", root, err)
	}
}

func TestBrowseCaps(t *testing.T) {
	ws := t.TempDir()
	for i := range terminals.MaxDirEntries + 5 {
		if err := os.Mkdir(filepath.Join(ws, "d"+string(rune('a'+i%26))+itoa(i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := (&Manager{workspace: ws}).Browse("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != terminals.MaxDirEntries || !got.Truncated {
		t.Fatalf("entries = %d truncated = %v, want %d and true", len(got.Entries), got.Truncated, terminals.MaxDirEntries)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}
