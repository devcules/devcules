package hostinfo

import "testing"

// TestParseMeminfoLine reads the value of a real /proc/meminfo line,
// not its key: abf457c parsed "MemTotal:" and always answered 0, so
// the status bar showed MEM 0.0G/0.0G.
func TestParseMeminfoLine(t *testing.T) {
	for line, want := range map[string]uint64{
		"MemTotal:        8136692 kB": 8136692 * 1024,
		"MemAvailable:    5021340 kB": 5021340 * 1024,
		"HugePages_Total:       0":    0,
		"HugePages_Free:       12":    12,
		"MemTotal:":                   0,
		"MemTotal:  lots kB":          0,
		"":                            0,
	} {
		if got := parseMeminfoUint(line); got != want {
			t.Errorf("parseMeminfoUint(%q) = %d, want %d", line, got, want)
		}
	}
	if total, used, pct := memStats(); total == 0 || used > total || pct <= 0 || pct > 100 {
		t.Errorf("memStats() = %d, %d, %.1f on this Linux box, want a real total", total, used, pct)
	}
}

// TestParseMeminfo checks the parse memStats runs on fixture bodies,
// including the ones that used to wrap used below zero or show a full
// bar: available above total, no MemAvailable, and a kB value too large
// to scale to bytes.
func TestParseMeminfo(t *testing.T) {
	const gib = 1024 * 1024 * 1024
	for _, c := range []struct {
		name        string
		body        string
		total, used uint64
		pct         float64
	}{
		{"normal", "MemTotal:  4194304 kB\nMemFree:  1048576 kB\nMemAvailable:  3145728 kB\nBuffers:  0 kB\n", 4 * gib, 1 * gib, 25},
		{"avail above total (container view)", "MemTotal:  2097152 kB\nMemAvailable:  4194304 kB\n", 2 * gib, 0, 0},
		{"no MemAvailable: free+buffers+cached", "MemTotal:  4194304 kB\nMemFree:  1048576 kB\nBuffers:  524288 kB\nCached:  524288 kB\nSwapCached:  999999 kB\n", 4 * gib, 2 * gib, 50},
		{"no MemAvailable, fallback above total", "MemTotal:  1048576 kB\nMemFree:  1048576 kB\nCached:  1048576 kB\n", 1 * gib, 0, 0},
		{"no MemAvailable and no MemFree", "MemTotal:  1048576 kB\n", 1 * gib, 0, 0},
		{"no MemTotal", "MemAvailable:  1048576 kB\n", 0, 0, 0},
		{"total overflows when scaled", "MemTotal:  18446744073709551615 kB\nMemAvailable:  1 kB\n", 0, 0, 0},
		{"avail overflows when scaled", "MemTotal:  1048576 kB\nMemAvailable:  18014398509481984 kB\n", 1 * gib, 1 * gib, 100},
		{"empty", "", 0, 0, 0},
	} {
		total, used, pct := parseMeminfo([]byte(c.body))
		if total != c.total || used != c.used || pct != c.pct {
			t.Errorf("%s: parseMeminfo = %d, %d, %.2f; want %d, %d, %.2f", c.name, total, used, pct, c.total, c.used, c.pct)
		}
		if used > total || pct < 0 || pct > 100 {
			t.Errorf("%s: used %d of %d (%.2f%%) is out of range", c.name, used, total, pct)
		}
	}
	if got := parseMeminfoUint("MemTotal:  18014398509481984 kB"); got != 0 {
		t.Errorf("parseMeminfoUint(huge kB) = %d, want 0", got)
	}
}
