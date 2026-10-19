//go:build !linux

package main

import (
	"context"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

// newProcSampleFunc returns a function that samples processes with ps(1)
// (macOS, the BSDs): its %cpu is the system's recent average.
func newProcSampleFunc() func() procTable {
	return func() procTable {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,rss=,%cpu=,comm=").Output()
		if err != nil {
			return procTable{err: "ps: " + err.Error()}
		}
		return parsePS(string(out))
	}
}

// memoryTotal is the machine's memory, from sysctl: hw.memsize on macOS,
// hw.physmem on the BSDs (0 if unknown).
func memoryTotal() uint64 {
	name := "hw.physmem"
	if runtime.GOOS == "darwin" {
		name = "hw.memsize"
	}
	n, err := unix.SysctlUint64(name)
	if err != nil {
		return 0
	}
	return n
}
