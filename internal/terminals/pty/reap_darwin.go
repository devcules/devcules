package pty

import (
	"bytes"
	"encoding/binary"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// listProcs reads the kernel's process table (kern.proc.all).
func listProcs() ([]proc, error) {
	ks, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	out := make([]proc, 0, len(ks))
	for _, k := range ks {
		out = append(out, proc{pid: int(k.Proc.P_pid), ppid: int(k.Eproc.Ppid), pgid: int(k.Eproc.Pgid)})
	}
	return out, nil
}

// procDetail reads a process's executable and environment from
// kern.procargs2 (argc, the exec path, argv, then the environment)
// and its working directory from proc_pidinfo; what it may not read
// stays empty.
func procDetail(pid int) (exe, cwd string, env []string) {
	if raw, err := unix.SysctlRaw("kern.procargs2", pid); err == nil && len(raw) > 4 {
		argc := int(binary.LittleEndian.Uint32(raw))
		parts := bytes.Split(raw[4:], []byte{0})
		i := 0
		if len(parts) > 0 {
			exe = string(parts[0])
			i = 1
		}
		for i < len(parts) && len(parts[i]) == 0 { // padding after the path
			i++
		}
		i += argc
		for ; i < len(parts) && len(parts[i]) > 0; i++ {
			env = append(env, string(parts[i]))
		}
	}
	return exe, darwinCwd(pid), env
}

// proc_pidinfo(PROC_PIDVNODEPATHINFO): struct proc_vnodepathinfo
// starts with the cwd's vnode_info (152 bytes) and path (MAXPATHLEN).
const (
	procInfoCallPidinfo    = 2
	procPidVnodePathInfo   = 9
	vnodeInfoSize          = 152
	maxPathLen             = 1024
	procVnodePathInfoBytes = 2 * (vnodeInfoSize + maxPathLen)
)

func darwinCwd(pid int) string {
	buf := make([]byte, procVnodePathInfoBytes)
	//lint:ignore SA1019 x/sys has no libSystem wrapper for proc_pidinfo
	n, _, e := syscall.Syscall6(unix.SYS_PROC_INFO, procInfoCallPidinfo, uintptr(pid), procPidVnodePathInfo, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if e != 0 || int(n) < vnodeInfoSize+1 {
		return ""
	}
	path := buf[vnodeInfoSize : vnodeInfoSize+maxPathLen]
	if i := bytes.IndexByte(path, 0); i >= 0 {
		path = path[:i]
	}
	return string(path)
}
