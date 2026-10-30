package pty

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// listProcs reads /proc: pid, then ppid and pgrp after the command
// name (which may hold spaces and parentheses).
func listProcs() ([]proc, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		i := bytes.LastIndexByte(stat, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(string(stat[i+1:]))
		if len(f) < 3 {
			continue
		}
		ppid, _ := strconv.Atoi(f[1])
		pgid, _ := strconv.Atoi(f[2])
		out = append(out, proc{pid: pid, ppid: ppid, pgid: pgid})
	}
	return out, nil
}

// procDetail reads a process's executable, working directory and
// environment; what it may not read stays empty.
func procDetail(pid int) (exe, cwd string, env []string) {
	base := "/proc/" + strconv.Itoa(pid)
	exe, _ = os.Readlink(base + "/exe")
	cwd, _ = os.Readlink(base + "/cwd")
	if raw, err := os.ReadFile(base + "/environ"); err == nil {
		for _, kv := range bytes.Split(raw, []byte{0}) {
			if len(kv) > 0 {
				env = append(env, string(kv))
			}
		}
	}
	return exe, cwd, env
}
