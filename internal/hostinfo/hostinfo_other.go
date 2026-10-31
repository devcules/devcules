//go:build !darwin && !linux

package hostinfo

import (
	"context"
	"os"
	"runtime"
	"time"
)

// Snapshot on a kernel devcules hasn't specialised reports the same
// static half of the line (name, OS/arch, cores) with the
// platform-probe fields zero; the client drops what serve didn't
// say. The local-run story here is the_repo line, always through git.
func snapshot() Info {
	hostname, _ := os.Hostname()
	info := Info{
		Hostname:  hostname,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Cores:     runtime.NumCPU(),
		Collected: time.Now(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if dir, err := os.Getwd(); err == nil {
		if root := repoRoot(ctx, dir); root != "" {
			info.Repo = root
			info.Branch = readBranch(ctx, root)
		}
	}
	return info
}
