//go:build unix

package pasture

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// startServer runs a server in this process on a private socket; the log
// goes to the test output.
func startServer(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pasture-test-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "s")
	tw := &testWriter{t: t}
	log := slog.New(slog.NewTextHandler(tw, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv, err := NewServer(Config{Socket: sock, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = srv.Serve(); close(done) }()
	t.Cleanup(func() {
		srv.Shutdown("test done")
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
		tw.mu.Lock()
		tw.off = true // stragglers must not log after the test ends
		tw.mu.Unlock()
		if _, err := os.Stat(sock); !os.IsNotExist(err) {
			t.Error("socket not removed")
		}
		os.RemoveAll(dir)
	})
	return sock
}

type testWriter struct {
	t   *testing.T
	mu  sync.Mutex
	off bool
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.off {
		w.t.Log(strings.TrimRight(string(p), "\n"))
	}
	return len(p), nil
}

func dial(t *testing.T, sock string) *Client {
	t.Helper()
	c, err := Dial(sock, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

var testEnv = []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color", "HOME=/tmp", "PS1=$ "}

func spawn(t *testing.T, c *Client, script string) *Pane {
	t.Helper()
	p, err := c.Spawn(SpawnOptions{Argv: []string{"/bin/sh", "-c", script}, Env: testEnv, Dir: "/tmp", Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// collector reads a pane's output in the background.
type collector struct {
	mu  sync.Mutex
	buf bytes.Buffer
	err error
}

func collect(p *Pane) *collector {
	col := &collector{}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := p.Read(b)
			col.mu.Lock()
			col.buf.Write(b[:n])
			if err != nil {
				col.err = err
				col.mu.Unlock()
				return
			}
			col.mu.Unlock()
		}
	}()
	return col
}

func (col *collector) String() string {
	col.mu.Lock()
	defer col.mu.Unlock()
	return col.buf.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitExit(t *testing.T, p *Pane) (int, error) {
	t.Helper()
	select {
	case <-p.Exited():
		return p.Wait()
	case <-time.After(5 * time.Second):
		t.Fatal("pane did not exit")
		return 0, nil
	}
}

func TestSpawnInputOutputExit(t *testing.T) {
	c := dial(t, startServer(t))
	p := spawn(t, c, `echo ready; read x; echo "got $x"; exit 3`)
	out := collect(p)
	waitFor(t, "ready", func() bool { return strings.Contains(out.String(), "ready") })
	if _, err := p.Write([]byte("abc\r")); err != nil {
		t.Fatal(err)
	}
	code, err := waitExit(t, p)
	if err != nil || code != 3 {
		t.Fatalf("exit %d, %v; want 3", code, err)
	}
	// All output arrives before the exit, then Read reports EOF.
	waitFor(t, "EOF", func() bool { out.mu.Lock(); defer out.mu.Unlock(); return out.err != nil })
	if !strings.Contains(out.String(), "got abc") || out.err != io.EOF {
		t.Fatalf("output %q, err %v", out.String(), out.err)
	}
	if list, _ := c.List(); len(list) != 0 {
		t.Fatalf("exited pane still listed: %+v", list)
	}
}

func TestDetachReattachSnapshot(t *testing.T) {
	sock := startServer(t)
	c1 := dial(t, sock)
	// Some history, a title, bracketed paste and mouse modes, then wait.
	p := spawn(t, c1, `i=1; while [ $i -le 40 ]; do echo line-$i; i=$((i+1)); done
printf '\033]2;my-title\007\033[?2004h\033[?1000h\033[?1006h'; printf 'prompt> '; exec sleep 60`)
	out := collect(p)
	waitFor(t, "prompt", func() bool { return strings.Contains(out.String(), "prompt> ") })
	pid := p.PID
	c1.Close() // goat quits: the program keeps running

	c2 := dial(t, sock)
	list, err := c2.List()
	if err != nil || len(list) != 1 || list[0].PID != pid || list[0].Title != "my-title" || list[0].Attached != 0 {
		t.Fatalf("list: %+v, %v", list, err)
	}
	p2, err := c2.Attach(list[0].ID, -1)
	if err != nil {
		t.Fatal(err)
	}
	if p2.Cols != 80 || p2.Rows != 24 {
		t.Fatalf("snapshot size %dx%d", p2.Cols, p2.Rows)
	}
	emu := vt.NewEmulator(p2.Cols, p2.Rows)
	emu.SetScrollbackSize(1000)
	go func() { _, _ = io.Copy(io.Discard, emu) }()
	_, _ = emu.Write(p2.Snapshot)
	screen := emu.String()
	if !strings.Contains(screen, "line-40") || !strings.Contains(screen, "prompt>") {
		t.Fatalf("screen after snapshot:\n%s", screen)
	}
	if emu.ScrollbackLen() == 0 || !strings.Contains(emu.Scrollback().Line(0).String(), "line-1") {
		t.Fatalf("history not restored (%d lines)", emu.ScrollbackLen())
	}
	if pos := emu.CursorPosition(); pos.X != len("prompt> ") {
		t.Fatalf("cursor at %v", pos)
	}
	for _, seq := range []string{"\x1b[?2004h", "\x1b[?1000h", "\x1b[?1006h", "my-title"} {
		if !strings.Contains(string(p2.Snapshot), seq) {
			t.Errorf("snapshot lacks %q", seq)
		}
	}
	// A second attach from the same client is refused.
	if _, err := c2.Attach(p2.ID, 0); err == nil {
		t.Error("double attach accepted")
	}
	if err := p2.Kill(); err != nil {
		t.Fatal(err)
	}
	if code, _ := waitExit(t, p2); code != 128+int(syscall.SIGHUP) {
		t.Errorf("killed pane status %d", code)
	}
}

func TestExitWhileDetached(t *testing.T) {
	sock := startServer(t)
	c1 := dial(t, sock)
	p := spawn(t, c1, `echo bye-now; sleep 0.3; exit 7`)
	out := collect(p)
	waitFor(t, "output", func() bool { return strings.Contains(out.String(), "bye-now") })
	if err := p.Detach(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Read(make([]byte, 10)); err != ErrClosed {
		t.Errorf("read after detach: %v", err)
	}
	time.Sleep(600 * time.Millisecond)
	list, _ := c1.List()
	if len(list) != 1 || !list[0].Dead || list[0].Status != 7 {
		t.Fatalf("dead pane not kept: %+v", list)
	}
	p2, err := c1.Attach(list[0].ID, -1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p2.Snapshot), "bye-now") {
		t.Errorf("snapshot of dead pane: %q", p2.Snapshot)
	}
	if code, err := waitExit(t, p2); code != 7 || err != nil {
		t.Errorf("dead pane status %d, %v", code, err)
	}
	if list, _ := c1.List(); len(list) != 0 {
		t.Errorf("dead pane not removed after delivery: %+v", list)
	}
}

func TestQueriesAnsweredOnlyWhenDetached(t *testing.T) {
	sock := startServer(t)
	c := dial(t, sock)
	// DSR 5 ("status?"); the answer is ESC [ 0 n. The program prints what
	// it receives.
	script := `stty raw -echo; sleep 0.3; printf '\033[5n'; dd bs=1 count=4 2>/dev/null | od -An -c; sleep 60`

	// Attached: the server must not answer; the client (its emulator) does.
	p := spawn(t, c, script)
	out := collect(p)
	waitFor(t, "query", func() bool { return strings.Contains(out.String(), "\x1b[5n") })
	time.Sleep(200 * time.Millisecond)
	_, _ = p.Write([]byte("XYZW"))
	waitFor(t, "echo of the client's answer", func() bool { return strings.Contains(out.String(), "X   Y   Z   W") })
	_ = p.Kill()

	// Detached: the server answers.
	p2 := spawn(t, c, script)
	if err := p2.Detach(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	p3, err := c.Attach(p2.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p3.Snapshot), "033   [") {
		t.Fatalf("server did not answer while detached; snapshot %q", p3.Snapshot)
	}
	_ = p3.Kill()
}

func TestResize(t *testing.T) {
	c := dial(t, startServer(t))
	p := spawn(t, c, `sleep 0.5; stty size; sleep 60`)
	out := collect(p)
	if err := p.Resize(100, 40); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "stty size", func() bool { return strings.Contains(out.String(), "40 100") })
	if err := p.Resize(0, 10); err == nil {
		t.Error("bad size accepted")
	}
	if err := p.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code, _ := waitExit(t, p); code != 128+int(syscall.SIGTERM) {
		t.Errorf("status after SIGTERM: %d", code)
	}
}

func TestBadRequests(t *testing.T) {
	sock := startServer(t)
	c := dial(t, sock)
	for _, o := range []SpawnOptions{
		{Cols: 80, Rows: 24},
		{Argv: []string{"/bin/sh"}, Cols: 0, Rows: 24},
		{Argv: []string{"/bin/sh"}, Cols: 80, Rows: 24, Dir: "relative"},
		{Argv: []string{"/bin/sh"}, Cols: 80, Rows: 24, Env: []string{"NOEQUALS"}},
		{Argv: []string{"/no/such/program"}, Cols: 80, Rows: 24},
	} {
		if _, err := c.Spawn(o); err == nil {
			t.Errorf("spawn %+v accepted", o)
		}
	}
	// A bare name is found through the PATH the client sends.
	if _, err := c.Spawn(SpawnOptions{Argv: []string{"sh", "-c", "exit 0"}, Env: []string{"PATH=/nonexistent"}, Cols: 80, Rows: 24}); err == nil {
		t.Error("spawn used the server's PATH instead of the client's")
	}
	if p, err := c.Spawn(SpawnOptions{Argv: []string{"sh", "-c", "exit 4"}, Env: []string{"PATH=/usr/bin:/bin"}, Cols: 80, Rows: 24}); err != nil {
		t.Errorf("spawn via client PATH: %v", err)
	} else if code, _ := waitExit(t, p); code != 4 {
		t.Errorf("status %d", code)
	}
	if _, err := c.Attach(999, 0); err == nil {
		t.Error("attach to unknown pane accepted")
	}
	if err := c.Kill(999); err == nil {
		t.Error("kill of unknown pane accepted")
	}
	if _, err := c.do(Request{Op: "bogus"}, nil); err == nil {
		t.Error("unknown op accepted")
	}
	// Still usable after errors.
	if _, err := c.List(); err != nil {
		t.Fatal(err)
	}

	// Wrong protocol version.
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = writeFrame(conn, msgHello, mustJSON(Hello{Version: ProtocolVersion + 1}))
	typ, payload, err := readFrame(conn)
	if err != nil || typ != msgResponse || !strings.Contains(string(payload), "version mismatch") {
		t.Errorf("version mismatch: %d %q %v", typ, payload, err)
	}
	// Garbage instead of a hello.
	conn2, _ := net.Dial("unix", sock)
	defer conn2.Close()
	_, _ = conn2.Write([]byte("GET / HTTP/1.0\r\n\r\n"))
	_ = conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn2.Read(make([]byte, 10)); err == nil {
		t.Error("garbage connection not closed")
	}
}

func TestSingleInstanceAndStaleSocket(t *testing.T) {
	sock := startServer(t)
	if _, err := NewServer(Config{Socket: sock}); err == nil {
		t.Fatal("second server on the same socket started")
	}
	// A stale socket file (crashed server, lock released) is replaced.
	dir, _ := os.MkdirTemp("", "pasture-stale-")
	defer os.RemoveAll(dir)
	_ = os.Chmod(dir, 0o700)
	stale := filepath.Join(dir, "s")
	l, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	srv, err := NewServer(Config{Socket: stale})
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	go srv.Serve()
	c, err := Dial(stale, "test")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	srv.Shutdown("test")
}

func TestServerStopEndsClients(t *testing.T) {
	dir, _ := os.MkdirTemp("", "pasture-stop-")
	defer os.RemoveAll(dir)
	_ = os.Chmod(dir, 0o700)
	sock := filepath.Join(dir, "s")
	srv, err := NewServer(Config{Socket: sock})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	c, err := Dial(sock, "test")
	if err != nil {
		t.Fatal(err)
	}
	p := spawn(t, c, "sleep 60")
	srv.Shutdown("test")
	if _, err := waitExit(t, p); err == nil {
		t.Error("pane Wait returned no error after the server stopped")
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client not closed")
	}
	if err := syscall.Kill(p.PID, 0); err == nil {
		time.Sleep(4 * time.Second) // SIGKILL grace
		if err := syscall.Kill(p.PID, 0); err == nil {
			t.Error("program still running after the server stopped")
		}
	}
}
