//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
)

// foregroundProcessName returns the command name of the PTY's foreground
// process group leader (e.g. "vim" while vim runs in the shell).
func foregroundProcessName(ptmx *os.File, cmd *exec.Cmd) string {
	pgrp, err := unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPGRP)
	if err != nil || pgrp <= 0 {
		if cmd.Process == nil {
			return ""
		}
		pgrp = cmd.Process.Pid
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pgrp))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
