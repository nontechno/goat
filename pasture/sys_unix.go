//go:build unix

package pasture

import (
	"fmt"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// checkPrivateDir refuses a socket directory that someone else owns or that
// others can enter.
func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by uid %d, not you", dir, st.Uid)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is accessible by others (mode %o); expected 0700", dir, fi.Mode().Perm())
	}
	return nil
}

// checkPeer accepts a connection only from the server's own user (the
// socket's 0700 directory already keeps others out; this is a second check).
func checkPeer(c net.Conn) error {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return nil
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	uid := -1
	var perr error
	if err := raw.Control(func(fd uintptr) { uid, perr = peerUID(int(fd)) }); err != nil {
		return err
	}
	if perr != nil {
		return perr
	}
	if uid >= 0 && uid != os.Getuid() {
		return fmt.Errorf("connection from uid %d refused (server runs as uid %d)", uid, os.Getuid())
	}
	return nil
}

// pollable returns a non-blocking duplicate of f, so that closing it wakes a
// goroutine blocked in Read (the Go runtime poller only manages
// non-blocking descriptors).
func pollable(f *os.File) *os.File {
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return f
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return f
	}
	nf := os.NewFile(uintptr(fd), f.Name())
	_ = f.Close()
	return nf
}

// ptyControl runs fn with the PTY's descriptor.
func ptyControl(f *os.File, fn func(fd int)) {
	if f == nil {
		return
	}
	if sc, err := f.SyscallConn(); err == nil {
		_ = sc.Control(func(fd uintptr) { fn(int(fd)) })
	}
}

// setWinsize sets the PTY's size (TIOCSWINSZ); the kernel then sends
// SIGWINCH to the PTY's foreground process group.
func setWinsize(f *os.File, cols, rows int) {
	ptyControl(f, func(fd int) {
		_ = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	})
}

// foregroundPgrp is the PTY's foreground process group (TIOCGPGRP), or 0.
func foregroundPgrp(ptmx *os.File) int {
	pgrp := 0
	ptyControl(ptmx, func(fd int) {
		if v, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP); err == nil {
			pgrp = v
		}
	})
	return pgrp
}

// lockFile takes an exclusive, non-blocking flock on path; the lock is held
// until the returned file is closed (or the process exits).
func lockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
