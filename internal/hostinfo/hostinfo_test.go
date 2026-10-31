package hostinfo

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// The package's contract: every Snapshot answers a hostname and the
// kernel shape; the probe fields (cpu, mem, gpu, branch) are
// best-effort but must stay in their expected bounds.
func TestSnapshot(t *testing.T) {
	info := Snapshot()
	if info.OS == "" || info.Arch == "" {
		t.Errorf("OS/Arch: %q %q", info.OS, info.Arch)
	}
	if info.Cores < 1 {
		t.Errorf("cores: %d", info.Cores)
	}
	// A build host's answers — we can't assert a number, only the shape.
	if info.CPUPercent < 0 || info.CPUPercent > 100 {
		t.Errorf("cpuPercent out of bounds: %v", info.CPUPercent)
	}
	if info.MemPercent < 0 || info.MemPercent > 100 {
		t.Errorf("memPercent out of bounds: %v", info.MemPercent)
	}
	// Running inside the repo's tree (go test's working dir is the
	// package dir) must surface the repo and branch lines.
	if info.Repo == "" {
		t.Error("repo: empty, expected the devcules checkout")
	}
	if info.Branch == "" {
		t.Errorf("branch: empty for repo %q", info.Repo)
	}
	if info.Collected.IsZero() {
		t.Error("collected: zero time")
	}
}

// repoRoot and readBranch answer or stay quiet: the contract is the
// checkout itself, not the name of the cwd.

func TestReadBranchReal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	branch := readBranch(ctx, ".")
	if branch == "" {
		t.Error("branch in package dir: empty, expected the checked-out branch")
	}
}

func TestRepoRootFromPackage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	root := repoRoot(ctx, ".")
	if root == "" {
		t.Error("repo root: empty")
	}
	if root != "" && !filepath.IsAbs(root) {
		t.Errorf("repo root not absolute: %q", root)
	}
}

func TestReadBranchNonRepo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	dir := t.TempDir()
	if got := readBranch(ctx, dir); got != "" {
		t.Errorf("branch in %s: %q, want empty", dir, got)
	}
	if got := repoRoot(ctx, dir); got != "" {
		t.Errorf("repo root in %s: %q, want empty", dir, got)
	}
}
