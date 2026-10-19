//go:build !linux

package main

import (
	"context"
	"os/exec"
	"time"
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
