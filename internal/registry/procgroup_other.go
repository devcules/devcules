//go:build !unix

package registry

import "os/exec"

// ownGroup is a no-op where there are no Unix process groups.
func ownGroup(*exec.Cmd) {}

// killGroup kills the process itself.
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
