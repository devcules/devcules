//go:build unix

package registry

import (
	"os/exec"
	"syscall"
)

// ownGroup starts cmd as the leader of a new session (and so of a new
// process group), with no controlling terminal: the `bash -ic` wrapper
// then runs the server inside that group without job control, and
// killGroup reaches the server and anything it spawned, not just bash.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// killGroup SIGKILLs cmd's whole process group (its pgid is its pid,
// see ownGroup).
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
