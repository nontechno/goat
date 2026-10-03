//go:build unix

package pasture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// A program inherits none of the server's descriptors: not other panes'
// PTY masters, the socket, the lock or history files.
func TestNoDescriptorsLeakIntoPrograms(t *testing.T) {
	if _, err := os.Stat("/proc/self/fd"); err != nil {
		t.Skip("needs /proc")
	}
	sock, _ := startServerWithHistory(t, 1<<20)
	c := dial(t, sock)
	p1 := spawn(t, c, "yes | head -n 5000; sleep 30") // its history goes to disk
	col1 := collect(p1)
	waitFor(t, "output", func() bool { return strings.Count(col1.String(), "y") >= 5000 })
	p2 := spawn(t, c, "ls -l /proc/$$/fd; exit 0")
	col := collect(p2)
	if _, err := waitExit(t, p2); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "output", func() bool { col.mu.Lock(); defer col.mu.Unlock(); return col.err != nil })
	out := col.String()
	for _, l := range strings.Split(out, "\n") {
		fd, target, ok := strings.Cut(strings.TrimSpace(l), " -> ")
		if !ok {
			continue
		}
		fd = fd[strings.LastIndexByte(fd, ' ')+1:]
		if fd != "0" && fd != "1" && fd != "2" {
			t.Errorf("program inherited descriptor %s -> %s", fd, target)
		}
	}
	_ = p1.Kill()
}

// Input acknowledgements for an earlier attachment don't reach a new one:
// detach and attach again while input is queued, then send a full window.
func TestReattachInputAcks(t *testing.T) {
	c := dial(t, startServer(t))
	p := spawn(t, c, "stty raw -echo; echo ready; sleep 1; cat >/dev/null")
	col := collect(p)
	waitFor(t, "ready", func() bool { return strings.Contains(col.String(), "ready") })
	buf := bytes.Repeat([]byte("a"), 1<<20)
	if _, err := p.Write(buf); err != nil {
		t.Fatal(err)
	}
	if err := p.Detach(); err != nil {
		t.Fatal(err)
	}
	p2, err := c.Attach(p.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	go io.Copy(io.Discard, p2)
	errc := make(chan error, 1)
	go func() {
		for range 4 {
			if _, err := p2.Write(buf); err != nil {
				errc <- err
				return
			}
		}
		errc <- nil
	}()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("write after reattach: %v (connection: %v)", err, c.Err())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("writes after reattach did not complete")
	}
	select {
	case <-c.Done():
		t.Fatalf("connection dropped: %v", c.Err())
	case <-time.After(500 * time.Millisecond):
	}
	_ = p2.Kill()
}

// A long hyperlink over many short runs doesn't blow up rendering; short
// links are kept as they are.
func TestRenderLimitsLinks(t *testing.T) {
	emu := vt.NewEmulator(80, 24)
	go func() {
		b := make([]byte, 4096)
		for {
			if _, err := emu.Read(b); err != nil {
				return
			}
		}
	}()
	url := "http://" + strings.Repeat("a", 1<<20)
	var in bytes.Buffer
	in.WriteString("\x1b]8;;" + url + "\x1b\\")
	for y := 1; y <= 24; y++ {
		fmt.Fprintf(&in, "\x1b[%d;1H%s", y, strings.Repeat("x", 80))
	}
	in.WriteString("\x1b]8;;\x1b\\")
	for y := 1; y <= 24; y++ {
		for x := 2; x <= 80; x += 2 {
			fmt.Fprintf(&in, "\x1b[%d;%dH ", y, x)
		}
	}
	_, _ = emu.Write(in.Bytes())
	row := func(y int) uv.Line {
		line := uv.NewLine(80)
		for x := range 80 {
			if c := emu.CellAt(x, y); c != nil {
				line[x] = *c
			}
		}
		return line
	}
	total := 0
	for y := range 24 {
		r := renderLine(row(y))
		total += len(r)
		if strings.Count(r, "x") != 40 {
			t.Fatalf("text lost: %q", r[:min(len(r), 200)])
		}
	}
	if total > 24*(lineLinkBytes+4096) {
		t.Errorf("rendered screen is %d bytes for %d bytes of output", total, in.Len())
	}

	// The same with a link that has only parameters (no URL).
	emu3 := vt.NewEmulator(80, 24)
	go func() { _, _ = io.Copy(io.Discard, emu3) }()
	_, _ = emu3.Write(bytes.Replace(in.Bytes(), []byte("\x1b]8;;"+url), []byte("\x1b]8;"+url[7:]+";"), 1))
	total = 0
	for y := range 24 {
		line := uv.NewLine(80)
		for x := range 80 {
			line[x] = *emu3.CellAt(x, y)
		}
		total += len(renderLine(line))
	}
	if total > 24*(lineLinkBytes+4096) {
		t.Errorf("params-only link: rendered screen is %d bytes", total)
	}

	// A short link, in 40 runs, is rendered in full each time.
	emu2 := vt.NewEmulator(80, 1)
	go func() { _, _ = io.Copy(io.Discard, emu2) }()
	_, _ = emu2.Write([]byte("\x1b]8;;http://example.com/x\x1b\\" + strings.Repeat("x", 80) + "\x1b]8;;\x1b\\"))
	for x := 2; x <= 80; x += 2 {
		fmt.Fprintf(emu2, "\x1b[1;%dH ", x)
	}
	line := uv.NewLine(80)
	for x := range 80 {
		line[x] = *emu2.CellAt(x, 0)
	}
	if n := strings.Count(renderLine(line), "http://example.com/x"); n != 40 {
		t.Errorf("short link rendered %d times, want 40", n)
	}
}

// A request that panics is answered with an error (the client waits for
// an answer), and the server carries on.
func TestRequestPanicAnswered(t *testing.T) {
	s := &Server{log: slog.New(slog.NewTextHandler(discard{}, nil)), panes: map[int]*pane{}, clients: map[*client]bool{}}
	c := &client{attached: map[*pane]*attachment{}, log: s.log, welcomed: true}
	c.cond = sync.NewCond(&c.mu)
	s.clients[c] = true
	s.panes[1] = &pane{id: 1, clients: map[*client]bool{}} // no emulator: attach panics
	req, _ := json.Marshal(Request{ID: 7, Op: OpAttach, Pane: 1})
	s.handleClientMsg(clientMsg{c: c, typ: msgRequest, payload: req})
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.queue) != 1 || c.queue[0][0] != msgResponse {
		t.Fatalf("no response queued: %d frames", len(c.queue))
	}
	var r Response
	if err := json.Unmarshal(c.queue[0][5:], &r); err != nil {
		t.Fatal(err)
	}
	if r.ID != 7 || !strings.Contains(r.Error, "internal error") {
		t.Errorf("response %+v", r)
	}
	if c.closed || c.stopped {
		t.Error("client dropped")
	}
}

// A file at the socket path that isn't a socket is not deleted; only our
// own history files are removed from the history directory.
func TestServerDeletesOnlyItsOwnFiles(t *testing.T) {
	dir, _ := os.MkdirTemp("", "pasture-own-")
	defer os.RemoveAll(dir)
	_ = os.Chmod(dir, 0o700)
	notes := filepath.Join(dir, "notes")
	_ = os.WriteFile(notes, []byte("keep me"), 0o600)
	if _, err := NewServer(Config{Socket: notes}); err == nil {
		t.Error("server started on a regular file")
	}
	if b, _ := os.ReadFile(notes); string(b) != "keep me" {
		t.Error("file at the socket path was deleted")
	}

	hist := filepath.Join(dir, "h")
	_ = os.MkdirAll(hist, 0o700)
	for _, n := range []string{"3-1.log", "app.log", "my-notes.log"} {
		_ = os.WriteFile(filepath.Join(hist, n), []byte("x"), 0o600)
	}
	lock, err := prepareHistoryDir(hist, slog.New(slog.NewTextHandler(discard{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	for n, want := range map[string]bool{"3-1.log": false, "app.log": true, "my-notes.log": true} {
		if _, err := os.Stat(filepath.Join(hist, n)); (err == nil) != want {
			t.Errorf("%s: kept=%v, want %v", n, err == nil, want)
		}
	}
}
