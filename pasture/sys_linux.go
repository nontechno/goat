//go:build linux

package pasture

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func peerUID(fd int) (int, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return -1, err
	}
	return int(cred.Uid), nil
}

// processName is the command name of pid (/proc/<pid>/comm).
func processName(pid int) string {
	if pid <= 0 {
		return ""
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// processDir is the working directory of pid.
func processDir(pid int) string {
	if pid <= 0 {
		return ""
	}
	d, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(d, " (deleted)")
}
