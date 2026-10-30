package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReap(t *testing.T) {
	var errs bytes.Buffer
	for _, args := range [][]string{nil, {"/etc"}, {"relative/launch-0123456789abcdef"}, {"/a/launch-0123456789abcdef", "extra"}} {
		errs.Reset()
		if code := run(append([]string{"reap"}, args...), &bytes.Buffer{}, &errs); code == exitOK || !strings.Contains(errs.String(), "launch dir") {
			t.Errorf("reap %q = %d %q, want a usage error", args, code, errs.String())
		}
	}
	// A launch dir nothing points into: nothing to stop, and fine.
	dir := filepath.Join(t.TempDir(), "launch-0123456789abcdef")
	if code := run([]string{"reap", dir}, &bytes.Buffer{}, &errs); code != exitOK {
		t.Fatalf("reap = %d %s", code, errs.String())
	}
}
