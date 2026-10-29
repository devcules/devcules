package pty

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePlace(t *testing.T) {
	ws := t.TempDir()
	m := &Manager{workspace: ws}
	if err := os.Mkdir(filepath.Join(ws, "shop"), 0o755); err != nil {
		t.Fatal(err)
	}

	dir, shell, err := m.resolve(Place{})
	if err != nil || dir != ws || strings.Join(shell, " ") != "bash -l" {
		t.Fatalf("default = %q %q %v", dir, shell, err)
	}
	dir, _, err = m.resolve(Place{Dir: "shop"})
	if err != nil || dir != filepath.Join(ws, "shop") {
		t.Fatalf("relative = %q %v", dir, err)
	}
	if _, _, err := m.resolve(Place{Dir: "missing"}); err == nil || err.Error() != "directory not found" {
		t.Fatalf("missing dir err = %v", err)
	}
	if _, _, err := m.resolve(Place{Dir: "../x"}); err == nil {
		t.Fatal("an escaping dir resolved")
	}
	// An existing checkout skips the clone.
	dir, shell, err = m.resolve(Place{Dir: "shop", Clone: "https://example.com/shop.git"})
	if err != nil || dir != filepath.Join(ws, "shop") || strings.Join(shell, " ") != "bash -l" {
		t.Fatalf("existing clone = %q %q %v", dir, shell, err)
	}
	// A new one clones from the parent (created) into the dir, quoted.
	dir, shell, err = m.resolve(Place{Dir: "src/new app", Clone: "https://example.com/a'b.git"})
	if err != nil {
		t.Fatalf("clone: %s", err)
	}
	if dir != filepath.Join(ws, "src") {
		t.Fatalf("clone starts in %q, want the parent", dir)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatal("clone parent was not created")
	}
	want := "git clone -- 'https://example.com/a'\\''b.git' '" + filepath.Join(ws, "src/new app") + "'; cd '" + filepath.Join(ws, "src/new app") + "' 2>/dev/null; exec bash -l"
	if len(shell) != 3 || shell[0] != "bash" || shell[1] != "-lc" || shell[2] != want {
		t.Fatalf("clone shell = %q\nwant %q", shell, want)
	}
	if _, _, err := m.resolve(Place{Dir: "x", Clone: "--upload-pack=evil"}); err == nil {
		t.Fatal("a flag passed as a clone URL")
	}
}
