//go:build unix

package pasture

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"syscall"
	"testing"
	"time"
)

func paneInfo(t *testing.T, c *Client, id int) (PaneInfo, bool) {
	t.Helper()
	list, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, pi := range list {
		if pi.ID == id {
			return pi, true
		}
	}
	return PaneInfo{}, false
}

// waitLong is waitFor for steps that move a lot of output (slow with -race).
func waitLong(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitExitLong(t *testing.T, p *Pane) (int, error) {
	t.Helper()
	select {
	case <-p.Exited():
		return p.Wait()
	case <-time.After(60 * time.Second):
		t.Fatal("pane did not exit")
		return 0, nil
	}
}

func (p *Pane) buffered() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.buf)
}

// A client that doesn't read holds the program back; nothing is lost, and
// the program's exit is reported after all its output.
func TestOutputFlowControl(t *testing.T) {
	sock := startServer(t)
	c := dial(t, sock)
	const n = 600000 // lines of "y": 1.2 MB from the program, 1.8 MB through the PTY
	p := spawn(t, c, fmt.Sprintf("yes 2>/dev/null | head -n %d; exit 5", n))

	waitLong(t, "output paused", func() bool {
		pi, _ := paneInfo(t, c, p.ID)
		return pi.Paused
	})
	time.Sleep(300 * time.Millisecond) // frames already on the way
	b1 := p.buffered()
	if b1 > outputWindow+(readAhead+1)*32<<10 {
		t.Errorf("client buffer %d beyond the window", b1)
	}
	time.Sleep(700 * time.Millisecond) // past the exit-drain deadline, too
	if b2 := p.buffered(); b2 != b1 {
		t.Errorf("output kept coming while paused: %d -> %d", b1, b2)
	}
	if pi, ok := paneInfo(t, c, p.ID); !ok || !pi.Paused {
		t.Errorf("pane not paused: %+v", pi)
	}
	select {
	case <-p.Exited():
		t.Fatal("exit reported before the output was read")
	default:
	}

	col := collect(p)
	code, err := waitExitLong(t, p)
	if err != nil || code != 5 {
		t.Fatalf("exit %d %v", code, err)
	}
	waitFor(t, "reader done", func() bool { col.mu.Lock(); defer col.mu.Unlock(); return col.err != nil })
	if o := strings.ReplaceAll(col.String(), "y\r\n", ""); o != "" {
		t.Errorf("unexpected output %q", o)
	}
	if got := strings.Count(col.String(), "y\r\n"); got != n {
		t.Errorf("got %d lines of output, want %d", got, n)
	}
}

// A slow reader on one client holds back output for all clients of the
// pane; once it detaches, the others get the rest.
func TestOutputFlowControlDetachResumes(t *testing.T) {
	sock := startServer(t)
	c1, c2 := dial(t, sock), dial(t, sock)
	p1 := spawn(t, c1, "sleep 0.3; yes 2>/dev/null | head -n 600000; exit 2")
	p2, err := c2.Attach(p1.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	col := collect(p2)
	waitLong(t, "output paused", func() bool {
		pi, _ := paneInfo(t, c2, p1.ID)
		return pi.Paused
	})
	if err := p1.Detach(); err != nil {
		t.Fatal(err)
	}
	if code, err := waitExitLong(t, p2); err != nil || code != 2 {
		t.Fatalf("exit %d %v", code, err)
	}
	waitFor(t, "reader done", func() bool { col.mu.Lock(); defer col.mu.Unlock(); return col.err != nil })
	if got := strings.Count(col.String(), "y\r\n"); got != 600000 {
		t.Errorf("got %d lines, want 600000", got)
	}
}

// Input the program doesn't read backs up in Pane.Write, not the server.
func TestInputFlowControl(t *testing.T) {
	sock := startServer(t)
	c := dial(t, sock)
	const n = 3000000
	p := spawn(t, c, fmt.Sprintf("stty raw -echo; echo ready; sleep 1.5; head -c %d | wc -c; exit 0", n))
	col := collect(p)
	waitFor(t, "ready", func() bool { return strings.Contains(col.String(), "ready") })

	wrote := make(chan error, 1)
	go func() {
		_, err := p.Write(bytes.Repeat([]byte("x"), n))
		wrote <- err
	}()
	time.Sleep(500 * time.Millisecond)
	select {
	case err := <-wrote:
		t.Fatalf("Write returned while the program was not reading: %v", err)
	default:
	}
	pi, _ := paneInfo(t, c, p.ID)
	if pi.InputQueued == 0 || pi.InputQueued > inputWindow+inputSlack {
		t.Errorf("server input queue %d (want 0 < q <= %d)", pi.InputQueued, inputWindow+inputSlack)
	}
	p.mu.Lock()
	inFlight := p.inFlight
	p.mu.Unlock()
	if inFlight > inputWindow {
		t.Errorf("input in flight %d beyond the window", inFlight)
	}

	select {
	case err := <-wrote:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Write did not complete")
	}
	if code, err := waitExit(t, p); err != nil || code != 0 {
		t.Fatalf("exit %d %v", code, err)
	}
	waitFor(t, "reader done", func() bool { col.mu.Lock(); defer col.mu.Unlock(); return col.err != nil })
	if !strings.Contains(col.String(), fmt.Sprint(n)) {
		t.Errorf("program read the wrong amount: %q", col.String())
	}
}

// A client that ignores the input window is disconnected; the pane stays.
func TestInputBeyondWindowDisconnects(t *testing.T) {
	sock := startServer(t)
	c := dial(t, sock)
	p := spawn(t, c, "stty raw -echo; echo ready; sleep 30")
	col := collect(p)
	waitFor(t, "ready", func() bool { return strings.Contains(col.String(), "ready") })
	chunk := bytes.Repeat([]byte("x"), maxInputChunk)
	for i := 0; i < (inputWindow+inputSlack)/maxInputChunk+4; i++ {
		if err := c.write(msgInput, encodePaneData(msgInput, p.ID, chunk)[5:]); err != nil {
			break
		}
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("client sending beyond the input window was not disconnected")
	}
	c2 := dial(t, sock)
	if _, ok := paneInfo(t, c2, p.ID); !ok {
		t.Fatal("pane gone after its client was disconnected")
	}
	_ = c2.Kill(p.ID)
}

type failWriter struct{ n int }

func (w *failWriter) Write(b []byte) (int, error) {
	w.n++
	return 0, syscall.EIO
}

// When writing to the program fails, queued and later input is dropped and
// acknowledged, instead of piling up.
func TestInputQueueClosesOnWriteError(t *testing.T) {
	q := newInputQueue(1)
	c := &client{stopped: true} // acknowledgements go nowhere
	a := &attachment{}
	for range 3 {
		a.inQueued.Add(10)
		q.add(c, a, make([]byte, 10))
	}
	if !q.reply([]byte("r")) {
		t.Fatal("reply refused")
	}
	w := &failWriter{}
	done := make(chan struct{})
	go func() { q.drainTo(w, slog.New(slog.DiscardHandler)); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not stop after a write error")
	}
	if w.n != 1 {
		t.Errorf("%d writes after the first failure", w.n-1)
	}
	if got := a.inQueued.Load(); got != 0 {
		t.Errorf("input not acknowledged: %d bytes", got)
	}
	a.inQueued.Add(5)
	q.add(c, a, make([]byte, 5))
	if got := a.inQueued.Load(); got != 0 || q.queued() != 0 {
		t.Errorf("input after close kept: in=%d queued=%d", got, q.queued())
	}
	if q.reply([]byte("r")) {
		t.Error("reply accepted after close")
	}
}

// A snapshot too big for a frame is sent without history; one that is too
// big even so is refused, and the pane is left as it was.
func TestSnapshotTooLarge(t *testing.T) {
	sock := startServer(t)
	c := dial(t, sock)
	p := spawn(t, c, `i=0; while [ $i -lt 500 ]; do echo "line $i ................................................"; i=$((i+1)); done; echo done; sleep 2; exit 3`)
	col := collect(p)
	waitFor(t, "output", func() bool { return strings.Contains(col.String(), "done") })
	if err := p.Detach(); err != nil {
		t.Fatal(err)
	}

	old := maxSnapshotFrame.Load()
	t.Cleanup(func() { maxSnapshotFrame.Store(old) })
	maxSnapshotFrame.Store(4096 + 12000) // the screen fits, 500 lines of history don't
	a, err := c.Attach(p.ID, 1000)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if bytes.Contains(a.Snapshot, []byte("line 100 ")) || !bytes.Contains(a.Snapshot, []byte("line 499 ")) {
		t.Errorf("snapshot should hold the screen only:\n%s", a.Snapshot)
	}
	pi, _ := paneInfo(t, c, p.ID)
	if a.HistoryFirst != pi.HistoryEnd {
		t.Errorf("snapshot history starts at %d, want %d (none)", a.HistoryFirst, pi.HistoryEnd)
	}
	if err := a.Detach(); err != nil {
		t.Fatal(err)
	}

	maxSnapshotFrame.Store(4096 + 100) // not even the screen
	if _, err := c.Attach(p.ID, 0); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized attach: %v", err)
	}
	if pi, ok := paneInfo(t, c, p.ID); !ok || pi.Attached != 0 {
		t.Fatalf("pane changed by a failed attach: %+v %v", pi, ok)
	}
	// A dead pane is kept too, until an attach can deliver it.
	waitFor(t, "dead", func() bool { pi, _ := paneInfo(t, c, p.ID); return pi.Dead })
	if _, err := c.Attach(p.ID, 0); err == nil {
		t.Fatal("oversized attach of a dead pane accepted")
	}
	if _, ok := paneInfo(t, c, p.ID); !ok {
		t.Fatal("dead pane removed by a failed attach")
	}
	maxSnapshotFrame.Store(old)
	d, err := c.Attach(p.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if code, err := waitExit(t, d); err != nil || code != 3 {
		t.Fatalf("exit %d %v", code, err)
	}
	if _, ok := paneInfo(t, c, p.ID); ok {
		t.Error("dead pane not removed once delivered")
	}
}
