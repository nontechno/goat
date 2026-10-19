//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO (or other special file) at local.toml is skipped without
// blocking: it is read on the event loop.
func TestHostFileNotRegular(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.toml")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	done := make(chan string, 1)
	go func() { done <- readBarRight(path).host }()
	select {
	case n := <-done:
		if want := cleanHostName(must(os.Hostname())); n != want {
			t.Errorf("got %q, want the system name %q", n, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading a FIFO at local.toml blocked")
	}
}
