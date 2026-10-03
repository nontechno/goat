//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// foregroundPid is the PTY's foreground process group leader (e.g. vim while
// vim runs in the shell), or the shell itself; 0 if neither is known.
func foregroundPid(ptmx *os.File, cmd *exec.Cmd) int {
	pgrp, err := 0, error(nil)
	ptyControl(ptmx, func(fd int) { pgrp, err = unix.IoctlGetInt(fd, unix.TIOCGPGRP) })
	if err == nil && pgrp > 0 {
		return pgrp
	}
	if cmd.Process != nil {
		return cmd.Process.Pid
	}
	return 0
}

// foregroundProcessName returns the command name of the PTY's foreground
// process group leader.
func foregroundProcessName(ptmx *os.File, cmd *exec.Cmd) string {
	pgrp := foregroundPid(ptmx, cmd)
	if pgrp == 0 {
		return ""
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pgrp))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// foregroundDir returns the working directory of the PTY's foreground
// process, falling back to the shell's ("" if neither can be read, e.g. a
// process of another user).
func foregroundDir(ptmx *os.File, cmd *exec.Cmd) string {
	pids := []int{foregroundPid(ptmx, cmd)}
	if cmd.Process != nil {
		pids = append(pids, cmd.Process.Pid)
	}
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		if d, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
			return strings.TrimSuffix(d, " (deleted)")
		}
	}
	return ""
}

// processEUID is the effective user id of pid (from /proc/<pid>/status).
func processEUID(pid int) (int, bool) {
	_, euid, ok := processUIDs(pid)
	return euid, ok
}

// processUIDs is the real and effective user id of pid.
func processUIDs(pid int) (ruid, euid int, ok bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, found := strings.CutPrefix(line, "Uid:"); found {
			f := strings.Fields(rest) // real, effective, saved, filesystem
			if len(f) >= 2 {
				r, err1 := strconv.Atoi(f[0])
				e, err2 := strconv.Atoi(f[1])
				if err1 == nil && err2 == nil {
					return r, e, true
				}
			}
		}
	}
	return 0, 0, false
}

// processArgs is the command line of pid.
func processArgs(pid int) []string {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
}
