//go:build unix

package pasture

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Programs start with default signal handling: SIGPIPE in particular must
// not be ignored (the server catches it for itself).
func TestProgramsGetDefaultSignals(t *testing.T) {
	c := dial(t, startServer(t))
	script := "yes | head -n 1"
	if _, err := os.Stat("/proc/self/status"); err == nil {
		script = "grep SigIgn /proc/self/status; " + script
	}
	p := spawn(t, c, script)
	col := collect(p)
	if _, err := waitExit(t, p); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "output", func() bool { col.mu.Lock(); defer col.mu.Unlock(); return col.err != nil })
	out := col.String()
	if strings.Contains(out, "Broken pipe") {
		t.Errorf("SIGPIPE ignored in programs: %q", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "SigIgn:"); ok {
			mask, err := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
			if err == nil && mask != 0 {
				t.Errorf("program starts with ignored signals: mask %#x", mask)
			}
		}
	}
}

// Stopping the server ends programs that ignore the hangup, too.
func TestServerStopKillsProgramsIgnoringHUP(t *testing.T) {
	dir, _ := os.MkdirTemp("", "pasture-stop-")
	defer os.RemoveAll(dir)
	_ = os.Chmod(dir, 0o700)
	sock := filepath.Join(dir, "s")
	srv, err := NewServer(Config{Socket: sock})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	go func() { _ = srv.Serve(); close(served) }()
	c, err := Dial(sock, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p := spawn(t, c, "trap '' HUP; echo ready; exec sleep 60")
	col := collect(p)
	waitFor(t, "ready", func() bool { return strings.Contains(col.String(), "ready") })
	srv.Shutdown("test")
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop")
	}
	if err := syscall.Kill(p.PID, 0); err == nil {
		t.Error("program ignoring SIGHUP still running after the server stopped")
		_ = syscall.Kill(p.PID, syscall.SIGKILL)
	}
}

// A relative program path is relative to the pane's directory.
func TestSpawnRelativePath(t *testing.T) {
	dir, _ := os.MkdirTemp("", "pasture-rel-")
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "prog"), []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := dial(t, startServer(t))
	p, err := c.Spawn(SpawnOptions{Argv: []string{"./prog"}, Env: testEnv, Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := waitExit(t, p); code != 7 {
		t.Errorf("status %d", code)
	}
}

func TestSignalAfterExitRefused(t *testing.T) {
	c := dial(t, startServer(t))
	p := spawn(t, c, "exit 0")
	if _, err := waitExit(t, p); err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(syscall.SIGTERM); err == nil {
		t.Error("signal to an exited pane accepted")
	}
}

// A program's last output is delivered even when the server is slow to
// process it: time the reader waits on the server doesn't count toward the
// exit deadline (which is for a child that keeps the PTY open).
func TestExitOutputDeliveredWhenServerBusy(t *testing.T) {
	c := dial(t, startServer(t))
	// Each output chunk takes 400 ms to process: when the program exits,
	// its last chunks are still in the PTY for longer than the deadline.
	feedDelay.Store(int64(400 * time.Millisecond))
	t.Cleanup(func() { feedDelay.Store(0) })
	const n = 60000
	p := spawn(t, c, "head -c 60000 /dev/zero | tr '\\0' x; exit 4")
	col := collect(p)
	code, err := waitExitLong(t, p)
	if err != nil || code != 4 {
		t.Fatalf("exit %d %v", code, err)
	}
	waitFor(t, "reader done", func() bool { col.mu.Lock(); defer col.mu.Unlock(); return col.err != nil })
	if got := strings.Count(col.String(), "x"); got != n {
		t.Errorf("got %d bytes of output before the exit, want %d", got, n)
	}
}
