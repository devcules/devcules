//go:build !linux && !darwin

package pty

import "errors"

// errNoProcs says this OS has no process reader: nothing is reaped.
var errNoProcs = errors.New("pty: process listing is not supported here")

func listProcs() ([]proc, error) { return nil, errNoProcs }

func procDetail(int) (exe, cwd string, env []string) { return "", "", nil }
