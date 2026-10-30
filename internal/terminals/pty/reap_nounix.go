//go:build !unix

package pty

// stop has no signals to send off unix.
func stop(_, _ []int, _ func(int) bool) {}
