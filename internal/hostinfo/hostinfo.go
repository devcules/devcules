// Package hostinfo reports the machine serve itself runs on: name,
// OS/arch, CPU use, memory pressure, and the current git branch of
// the repo it was started in (the runner roots at that directory
// in `devcules local`, so "the project" is this project's checkout).

// The bar is a convenience panel, not monitoring: each field is best
// effort. CPU is measured over the process's own run, not sampled
// from the kernel; a metric the OS won't say is absent from the
// JSON, and the client drops the whole bar on a fetch failure.
package hostinfo

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Info is the status-bar payload served at /api/host-info.
type Info struct {
	// Kernel shape, straight from the runtime.
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`

	// CPU is a float percentage of the machine's logical CPUs in
	// use, sampled over Snapshot's own sample window (the float
	// holds tenths of a percent to stay readable on the bar).
	CPUPercent float64 `json:"cpuPercent"`
	// Cores is the machine's logical CPU count.
	Cores int `json:"cores"`

	// Memory, from the same kernel the process runs on. Total and
	// Used are bytes; Percent is the bar's number.
	MemTotal   uint64  `json:"memTotalBytes"`
	MemUsed    uint64  `json:"memUsedBytes"`
	MemPercent float64 `json:"memPercent"`

	// Repo is the repo root the serve process was started in (the
	// runner's root in `devcules local`); Branch its current
	// checked-out branch. Empty on any of the many ways this isn't
	// a checkout — a treeless worktree, a detached head, a plain
	// directory, gitless serve.
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`

	// GPUName is a display name when there's a GPU answer on this
	// OS, and empty otherwise; the client renders nothing for a
	// field serve didn't say.
	GPUName string `json:"gpuName,omitempty"`

	// Collected is when the payload was built, so a stale bar
	// settles quietly (the client pauses on hidden tabs and shows
	// what it last read).
	Collected time.Time `json:"collected"`
}

// Snapshot reports this process's machine. The CPU sample is a
// short proc/stat or equivalent read-and-delta; an empty GPU name
// isn't an error.
func Snapshot() Info { return snapshot() }

// readBranch reads the repo's current checked-out branch, given a
// directory in the repo. Any failure leaves it empty: a worktree
// without a git file, a detached head, git not installed.
func readBranch(ctx context.Context, dir string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", dir,
		"rev-parse", "--abbrev-ref", "HEAD",
	)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// repoRoot is the "repo" line for the bar: `git rev-parse
// --show-toplevel` says the directory even in a shallow or linked
// worktree. Empty when dir isn't inside a checkout.
func repoRoot(ctx context.Context, dir string) string {
	cmd := exec.CommandContext(ctx, "git", "-C", dir,
		"rev-parse", "--show-toplevel",
	)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
