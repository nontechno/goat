//go:build darwin

package main

import (
	"bytes"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/unix"
)

// macOS has no /proc: the foreground process group comes from the PTY
// (TIOCGPGRP), a process's name, user and command line from sysctl, and
// its working directory from proc_pidinfo. (Unlike the shell's OSC 7, this
// needs nothing from the shell: macOS's own shell setup only sends OSC 7
// inside Terminal.app.)

// foregroundPid is the PTY's foreground process group leader, or the shell
// itself; 0 if neither is known.
func foregroundPid(ptmx *os.File, cmd *exec.Cmd) int {
	pgrp, err := 0, error(nil)
	ptyControl(ptmx, func(fd int) { pgrp, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP) })
	if err == nil && pgrp > 0 {
		return pgrp
	}
	if cmd != nil && cmd.Process != nil {
		return cmd.Process.Pid
	}
	return 0
}

// kinfo is pid's kernel process record.
func kinfo(pid int) (*unix.KinfoProc, bool) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || k == nil || k.Proc.P_pid != int32(pid) {
		return nil, false
	}
	return k, true
}

// foregroundProcessName is the command name of the PTY's foreground
// process group leader.
func foregroundProcessName(ptmx *os.File, cmd *exec.Cmd) string {
	pid := foregroundPid(ptmx, cmd)
	if pid == 0 {
		return ""
	}
	k, ok := kinfo(pid)
	if !ok {
		return ""
	}
	return string(bytes.TrimRight(k.Proc.P_comm[:], "\x00"))
}

// foregroundDir is the working directory of the PTY's foreground process,
// else the shell's ("" if neither can be read).
func foregroundDir(ptmx *os.File, cmd *exec.Cmd) string {
	pids := []int{foregroundPid(ptmx, cmd)}
	if cmd != nil && cmd.Process != nil {
		pids = append(pids, cmd.Process.Pid)
	}
	for _, pid := range pids {
		if d := processCwd(pid); d != "" {
			return d
		}
	}
	return ""
}

// processCwd asks proc_pidinfo(PROC_PIDVNODEPATHINFO) for pid's current
// directory. struct proc_vnodepathinfo is two vnode_info_path (the current
// and the root directory), each a 152-byte vnode_info and a MAXPATHLEN path.
func processCwd(pid int) string {
	const (
		callPidInfo       = 2 // PROC_INFO_CALL_PIDINFO
		pidVnodePathInfo  = 9 // PROC_PIDVNODEPATHINFO
		vnodeInfoSize     = 152
		maxPathLen        = 1024
		vnodePathInfoSize = 2 * (vnodeInfoSize + maxPathLen)
	)
	if pid <= 0 {
		return ""
	}
	var buf [vnodePathInfoSize]byte
	n, _, errno := unix.Syscall6(unix.SYS_PROC_INFO, callPidInfo, uintptr(pid), pidVnodePathInfo, 0,
		uintptr(unsafe.Pointer(&buf[0])), vnodePathInfoSize)
	if errno != 0 || int(n) < vnodeInfoSize+maxPathLen {
		return ""
	}
	path := buf[vnodeInfoSize : vnodeInfoSize+maxPathLen]
	if i := bytes.IndexByte(path, 0); i >= 0 {
		path = path[:i]
	}
	if len(path) == 0 || path[0] != '/' {
		return ""
	}
	return string(path)
}

// processEUID is the effective user id of pid.
func processEUID(pid int) (int, bool) {
	_, euid, ok := processUIDs(pid)
	return euid, ok
}

// processUIDs is the real and effective user id of pid.
func processUIDs(pid int) (ruid, euid int, ok bool) {
	k, ok := kinfo(pid)
	if !ok {
		return 0, 0, false
	}
	return int(k.Eproc.Pcred.P_ruid), int(k.Eproc.Ucred.Uid), true
}

// processArgs is the command line of pid (kern.procargs2, parseProcArgs2).
func processArgs(pid int) []string {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil
	}
	return parseProcArgs2(b)
}
