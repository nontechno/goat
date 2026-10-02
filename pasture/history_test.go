//go:build unix

package pasture

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testHistory(t *testing.T, maxBytes int64) *history {
	t.Helper()
	dir := t.TempDir()
	return newHistory(HistoryConfig{Dir: dir, MaxBytes: maxBytes}, 7, slog.New(slog.NewTextHandler(discard{}, nil)))
}

func TestHistoryStoreReadPages(t *testing.T) {
	h := testHistory(t, 1<<30)
	const n = 3000
	for i := 0; i < n; i++ {
		h.add(fmt.Sprintf("line-%d", i))
	}
	if h.base != 0 || h.end != n {
		t.Fatalf("bounds %d-%d", h.base, h.end)
	}
	// Any range, including ones crossing the sparse index points.
	for _, r := range [][2]int64{{0, 1}, {255, 257}, {1000, 1300}, {2990, 3000}, {0, n}} {
		got, err := h.read(r[0], r[1], 1<<30)
		if err != nil {
			t.Fatal(err)
		}
		if int64(len(got)) != r[1]-r[0] {
			t.Fatalf("%v: %d lines", r, len(got))
		}
		for i, l := range got {
			if want := fmt.Sprintf("line-%d", r[0]+int64(i)); l != want {
				t.Fatalf("%v: line %d = %q, want %q", r, i, l, want)
			}
		}
	}
	// The byte budget stops early, but always returns one line.
	got, _ := h.read(0, 100, 20) // "line-0" is 6 bytes: 3 fit
	if len(got) != 3 {
		t.Fatalf("budget 20: %d lines", len(got))
	}
	got, _ = h.read(0, 100, 1)
	if len(got) != 1 {
		t.Fatalf("budget 1: %d lines", len(got))
	}
	// Newlines in a line can't break the records.
	h.add("a\nb\rc")
	got, _ = h.read(n, n+1, 1<<20)
	if len(got) != 1 || got[0] != "a b c" {
		t.Fatalf("sanitized line: %q", got)
	}
	h.close()
	if files, _ := filepath.Glob(filepath.Join(h.dir, "*")); len(files) != 0 {
		t.Fatalf("files left after close: %v", files)
	}
}

func TestHistoryStoreLimitDropsOldestSegments(t *testing.T) {
	const limit = 1 << 20
	h := testHistory(t, limit)
	line := strings.Repeat("x", 1000)
	total := int64(0)
	for total < 6*limit {
		h.add(line)
		total += int64(len(line) + 1)
	}
	// Within the limit plus at most one segment (a quarter of it).
	if h.total > limit+limit/4+int64(len(line)+1) {
		t.Fatalf("on disk %d bytes, limit %d", h.total, limit)
	}
	if h.base == 0 || h.base >= h.end {
		t.Fatalf("oldest line %d of %d: nothing dropped?", h.base, h.end)
	}
	files, _ := filepath.Glob(filepath.Join(h.dir, "*.log"))
	if len(files) != len(h.segs) || len(files) > 6 {
		t.Fatalf("%d files, %d segments", len(files), len(h.segs))
	}
	// Reading before the oldest line returns from the oldest.
	got, err := h.read(0, h.base+2, 1<<30)
	if err != nil || len(got) != 2 {
		t.Fatalf("read across the dropped part: %d lines, %v", len(got), err)
	}
	// A clear drops everything, numbering continues.
	h.discard(h.end + 10)
	if h.base != h.end || len(h.segs) != 0 || h.total != 0 {
		t.Fatalf("after discard: %d-%d, %d segments", h.base, h.end, len(h.segs))
	}
	if files, _ := filepath.Glob(filepath.Join(h.dir, "*.log")); len(files) != 0 {
		t.Fatalf("files after discard: %v", files)
	}
}

func TestHistoryStoreOff(t *testing.T) {
	h := newHistory(HistoryConfig{}, 1, slog.New(slog.NewTextHandler(discard{}, nil)))
	h.add("a")
	h.add("b")
	if h.base != 2 || h.end != 2 {
		t.Fatalf("off: %d-%d", h.base, h.end)
	}
}

func TestPrepareHistoryDirRemovesStaleFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "h")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "3-1.log"), []byte("old\n"), 0o600)
	if err := prepareHistoryDir(dir, slog.New(slog.NewTextHandler(discard{}, nil))); err != nil {
		t.Fatal(err)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "*")); len(files) != 0 {
		t.Fatalf("stale files kept: %v", files)
	}
}

// End to end: a pane keeps 100 lines in memory; the rest goes to disk, and
// paging backwards returns every line once, in order.
func TestHistoryPagingThroughDisk(t *testing.T) {
	sock, histDir := startServerWithHistory(t, 64<<20)
	c := dial(t, sock)
	const n = 5000
	p, err := c.Spawn(SpawnOptions{
		Argv: []string{"/bin/sh", "-c", fmt.Sprintf("i=0; while [ $i -lt %d ]; do echo \"L$i\"; i=$((i+1)); done; echo done-printing; sleep 60", n)},
		Env:  testEnv, Dir: "/tmp", Cols: 40, Rows: 10, History: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := collect(p)
	waitFor(t, "output", func() bool { return strings.Contains(out.String(), "done-printing") })

	info := func() PaneInfo {
		list, err := c.List()
		if err != nil || len(list) != 1 {
			t.Fatalf("list: %v %v", list, err)
		}
		return list[0]
	}
	waitFor(t, "history", func() bool { return info().HistoryEnd >= n-10 })
	pi := info()
	if pi.HistoryOldest != 0 || pi.HistoryDiskBytes == 0 {
		t.Fatalf("history %d-%d, %d bytes on disk", pi.HistoryOldest, pi.HistoryEnd, pi.HistoryDiskBytes)
	}
	if files, _ := filepath.Glob(filepath.Join(histDir, "*.log")); len(files) == 0 {
		t.Fatal("no history files on disk")
	}

	// Page from the newest back to the oldest, 700 lines at a time.
	var all []string
	before := HistoryEnd
	for pages := 0; ; pages++ {
		page, err := c.History(p.ID, before, 700)
		if err != nil {
			t.Fatal(err)
		}
		if pages > 100 {
			t.Fatal("paging does not end")
		}
		all = append(page.Lines, all...)
		if !page.More() {
			break
		}
		before = page.First
	}
	// The shell's command line output: L0..L(n-1) are in order, each once.
	var ls []string
	for _, l := range all {
		if strings.HasPrefix(l, "L") {
			ls = append(ls, l)
		}
	}
	if len(ls) < n-10 { // the last few lines may still be on the screen
		t.Fatalf("got %d L-lines", len(ls))
	}
	for i, l := range ls {
		if l != fmt.Sprintf("L%d", i) {
			t.Fatalf("line %d is %q", i, l)
		}
	}

	// A reattach gets recent history in the snapshot and where it starts.
	c2 := dial(t, sock)
	p2, err := c2.Attach(p.ID, 300)
	if err != nil {
		t.Fatal(err)
	}
	if p2.HistoryOldest != 0 || p2.HistoryFirst != pi.HistoryEnd-300 {
		t.Fatalf("snapshot history from %d (oldest %d), end %d", p2.HistoryFirst, p2.HistoryOldest, pi.HistoryEnd)
	}
	if !strings.Contains(string(p2.Snapshot), fmt.Sprintf("L%d", n-1)) {
		t.Fatal("snapshot lacks the newest line")
	}

	// When the pane goes, so do its files.
	_ = p.Kill()
	waitExit(t, p)
	waitFor(t, "history files removed", func() bool {
		files, _ := filepath.Glob(filepath.Join(histDir, "*.log"))
		return len(files) == 0
	})
}

func TestHistoryClearDiscardsDisk(t *testing.T) {
	sock, histDir := startServerWithHistory(t, 64<<20)
	c := dial(t, sock)
	p, err := c.Spawn(SpawnOptions{
		Argv: []string{"/bin/sh", "-c", "i=0; while [ $i -lt 500 ]; do echo L$i; i=$((i+1)); done; sleep 0.3; printf '\\033[3J'; echo after-clear; sleep 60"},
		Env:  testEnv, Dir: "/tmp", Cols: 40, Rows: 10, History: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := collect(p)
	waitFor(t, "clear", func() bool { return strings.Contains(out.String(), "after-clear") })
	time.Sleep(100 * time.Millisecond)
	list, _ := c.List()
	// Everything before the clear is gone (only what scrolled off after it,
	// at most a line or two, remains).
	if len(list) != 1 || list[0].HistoryOldest < 490 || list[0].HistoryEnd-list[0].HistoryOldest > 2 ||
		list[0].HistoryDiskBytes != 0 {
		t.Fatalf("after clear: %+v", list)
	}
	if files, _ := filepath.Glob(filepath.Join(histDir, "*.log")); len(files) != 0 {
		t.Fatalf("history files after clear: %v", files)
	}
	page, err := c.History(p.ID, HistoryEnd, 100)
	if err != nil || len(page.Lines) > 2 || page.More() || strings.Contains(strings.Join(page.Lines, ""), "L1") {
		t.Fatalf("history after clear: %+v %v", page, err)
	}
	_ = p.Kill()
}

func startServerWithHistory(t *testing.T, maxBytes int64) (sock, histDir string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "pasture-test-")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(dir, 0o700)
	sock = filepath.Join(dir, "s")
	histDir = sock + ".history"
	srv, err := NewServer(Config{Socket: sock, History: HistoryConfig{Dir: histDir, MaxBytes: maxBytes}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = srv.Serve(); close(done) }()
	t.Cleanup(func() {
		srv.Shutdown("test done")
		<-done
		os.RemoveAll(dir)
	})
	return sock, histDir
}
