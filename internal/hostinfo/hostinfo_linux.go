//go:build linux

package hostinfo

import (
	"context"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Snapshot reports the Linux shape of the bar. /proc answers all of
// it without an external process, over a 100ms CPU sample.
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
	info.MemTotal, info.MemUsed, info.MemPercent = memStats()
	info.CPUPercent = cpuSample()
	info.GPUName = gpuName()
	return info
}

// memStats reads /proc/meminfo and answers (total, used,
// percent-of-100); see parseMeminfo.
func memStats() (total, used uint64, pct float64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, 0
	}
	return parseMeminfo(data)
}

// parseMeminfo answers (total, used, percent-of-100) from a
// /proc/meminfo body: used is MemTotal less MemAvailable. A kernel
// before 3.14 has no MemAvailable; MemFree+Buffers+Cached stands in,
// and with neither, used is 0 rather than a full bar. Available is
// capped at total (a container or LXCFS view can report more), so used
// never wraps below zero.
func parseMeminfo(data []byte) (total, used uint64, pct float64) {
	var avail, free, buffers, cached uint64
	var haveAvail, haveFree bool
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			total = parseMeminfoUint(line)
		case strings.HasPrefix(line, "MemAvailable:"):
			avail, haveAvail = parseMeminfoUint(line), true
		case strings.HasPrefix(line, "MemFree:"):
			free, haveFree = parseMeminfoUint(line), true
		case strings.HasPrefix(line, "Buffers:"):
			buffers = parseMeminfoUint(line)
		case strings.HasPrefix(line, "Cached:"):
			cached = parseMeminfoUint(line)
		}
	}
	if total == 0 {
		return 0, 0, 0
	}
	switch {
	case haveAvail:
	case haveFree:
		avail = satAdd(satAdd(free, buffers), cached)
	default:
		avail = total
	}
	avail = min(avail, total)
	used = total - avail
	pct = float64(used) / float64(total) * 100
	return total, used, pct
}

// satAdd adds without wrapping past the largest uint64.
func satAdd(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}

// parseMeminfoUint reads one /proc/meminfo line ("MemTotal:  16367772 kB")
// and answers its value in bytes: the field after the key, scaled when
// the unit is kB (meminfo has used kB since 2.6). A line it cannot
// read, or a kB value too large to scale, answers 0.
func parseMeminfoUint(line string) uint64 {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0
	}
	n, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	if len(fields) > 2 && fields[2] == "kB" {
		if n > math.MaxUint64/1024 {
			return 0
		}
		return n * 1024
	}
	return n
}

// cpuSample reads /proc/stat's aggregate line twice, about 100ms
// apart, and divides the deltas. Linux tracks /proc/stat since
// boot; only the delta is live.
func cpuSample() float64 {
	one := readProcStat()
	time.Sleep(100 * time.Millisecond)
	two := readProcStat()
	busy := float64(two.user+two.nice+two.system+two.irq+two.softirq+two.steal) -
		float64(one.user+one.nice+one.system+one.irq+one.softirq+one.steal)
	total := float64(two.total - one.total)
	if total <= 0 || busy < 0 {
		return 0
	}
	pct := busy / total * 100
	if pct > 100 {
		return 100
	}
	return pct
}

type procStatLine struct {
	user, nice, system, idle, iowait, irq, softirq, steal uint64
	total                                                 uint64
}

// readProcStat parses the first (aggregate) "cpu" line of /proc/stat.
// The kernel's uptime columns count USER_HZ ticks (0.01 seconds);
// the ratio alone matters so we never scale.
func readProcStat() procStatLine {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return procStatLine{}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		l := procStatLine{}
		vals := []*uint64{&l.user, &l.nice, &l.system, &l.idle, &l.iowait, &l.irq, &l.softirq, &l.steal}
		for i, f := range fields[1 : 1+len(vals)] {
			if i >= len(vals) {
				break
			}
			*vals[i] = parseUint(f)
			l.total += *vals[i]
		}
		return l
	}
	return procStatLine{}
}

// parseUint is one unsigned token, zero on any failure (the
// /proc/stat column's the kernel's own formatting).
func parseUint(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}

// gpuName walks /sys's display-pipeline dirs for a vendor name.
// No dep, so it's best-effort: a GPU with a familiar driver name
// is named in the bar and the absence leaves it blank.
func gpuName() string {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c",
		"ls /sys/class/drm 2>/dev/null | grep -E '^card[0-9]+$' | head -1").Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return ""
	}
	card := strings.TrimSpace(string(out))
	vendorBytes, err := os.ReadFile("/sys/class/drm/" + card + "/device/vendor")
	if err != nil {
		return card
	}
	return card + " " + strings.TrimSpace(string(vendorBytes))
}
