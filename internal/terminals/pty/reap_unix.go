//go:build unix

package pty

import (
	"slices"
	"syscall"
	"time"
)

// stop signals pids and groups with SIGTERM, waits up to reapGrace for
// them to go, then SIGKILLs what is left. still, when set, rechecks a
// pid before the SIGKILL.
func stop(pids, groups []int, still func(int) bool) {
	if len(pids) == 0 && len(groups) == 0 {
		return
	}
	for _, g := range groups {
		_ = syscall.Kill(-g, syscall.SIGTERM)
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	alive := func(pid int) bool { return syscall.Kill(pid, 0) == nil }
	for deadline := time.Now().Add(reapGrace); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if !slices.ContainsFunc(pids, alive) && !slices.ContainsFunc(groups, alive) {
			return
		}
	}
	for _, g := range groups {
		_ = syscall.Kill(-g, syscall.SIGKILL)
	}
	for _, pid := range pids {
		if alive(pid) && (still == nil || still(pid)) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
