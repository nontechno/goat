//go:build darwin || freebsd

package pasture

import "golang.org/x/sys/unix"

func peerUID(fd int) (int, error) {
	// SOL_LOCAL is 0 on both systems.
	cred, err := unix.GetsockoptXucred(fd, 0, unix.LOCAL_PEERCRED)
	if err != nil {
		return -1, err
	}
	return int(cred.Uid), nil
}

// processName and processDir need /proc; on these systems window names
// fall back to program-set titles and directories to what the shell
// reports with OSC 7.
func processName(int) string { return "" }
func processDir(int) string  { return "" }
