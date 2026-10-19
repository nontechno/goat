//go:build !windows

package main

import (
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// What goat needs from a Unix system (sys_windows.go has the stand-ins
// that let goat build on Windows, where it doesn't run).

// resizeSignals are the signals telling goat its terminal changed size.
func resizeSignals() []os.Signal { return []os.Signal{syscall.SIGWINCH} }

// processCPUTime is goat's own CPU time so far (user + system).
func processCPUTime() (time.Duration, bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), true
}

// hangUpGroup sends SIGHUP to a window's whole process group (its shell and
// the jobs started from it), as closing a terminal does.
func hangUpGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGHUP) }

// canEnter reports whether goat may make d its current directory (X_OK).
func canEnter(d string) bool { return syscall.Access(d, 1 /* X_OK */) == nil }

// unsupportedPlatform is why goat can't run here ("": it can).
func unsupportedPlatform() string { return "" }

// pollable returns the PTY master as a non-blocking file managed by Go's
// poller. creack/pty hands it over in blocking mode (its ioctls call Fd(),
// which switches a file to blocking), and then Close can't interrupt the
// reader's pending Read: a closed window's reader goroutine and PTY would
// live on for as long as anything (a job that ignores SIGHUP) keeps the
// terminal open. Afterwards, Fd() must not be called on it again; ioctls go
// through ptyControl.
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

// ptyControl runs fn with the PTY's descriptor without making it blocking.
func ptyControl(f *os.File, fn func(fd int)) {
	if f == nil {
		return
	}
	if sc, err := f.SyscallConn(); err == nil {
		_ = sc.Control(func(fd uintptr) { fn(int(fd)) })
	}
}

// setWinsize tells the program in the PTY its window size (TIOCSWINSZ).
func setWinsize(f *os.File, cols, rows int) {
	ptyControl(f, func(fd int) {
		_ = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	})
}
