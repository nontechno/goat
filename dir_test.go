package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestFitAndHome(t *testing.T) {
	for _, c := range []struct {
		in   string
		w    int
		want string
	}{
		{"/usr/local/src", 20, "/usr/local/src"},
		{"/usr/local/src", 14, "/usr/local/src"},
		{"/usr/local/src", 8, "…cal/src"},
		{"/usr/local/src", 1, ""},
		{"/x/日本語", 4, "…語"}, {"/x/日本語", 5, "…本語"}, // wide characters are never split
	} {
		if got := fitEnd(c.in, c.w); got != c.want {
			t.Errorf("fitEnd(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
	if got := fitStart("copied 12 characters", 8); got != "copied …" {
		t.Errorf("fitStart = %q", got)
	}
	for in, want := range map[string]string{
		"/home/sp": "~", "/home/sp/src/goat": "~/src/goat", "/home/spx": "/home/spx", "/etc": "/etc",
	} {
		if got := homeShort(in, "/home/sp"); got != want {
			t.Errorf("homeShort(%q) = %q, want %q", in, got, want)
		}
	}
	if parseOSC7("file://host/home/a%20b") != "/home/a b" || parseOSC7("junk") != "" {
		t.Error("parseOSC7")
	}
}

func TestStatusShowsDir(t *testing.T) {
	m, _, _, b := wmWithBackground(t)
	m.cfg.StatusShowDir = true
	m.home = "/home/sp"
	b.dir = "/home/sp/src/github.com/nontechno/goat"
	m.resizeScreen(90, 12)
	status := render(m)[11]
	clock := time.Now().Format("15:04")
	if !strings.Contains(status, "~/src/github.com/nontechno/goat "+clock) {
		t.Fatalf("status %q: want the directory next to the clock", status)
	}
	// Too narrow: tabs and clock stay whole, the directory keeps its end.
	m.resizeScreen(40, 12)
	status = render(m)[11]
	if !strings.Contains(status, "0:bash") || !strings.Contains(status, "…") ||
		!strings.Contains(status, "goat "+clock) {
		t.Fatalf("narrow status %q", status)
	}
	// A message and the directory share the space; the message comes first.
	m.resizeScreen(90, 12)
	m.setStatus("copied 5 characters", time.Hour)
	status = render(m)[11]
	if !strings.Contains(status, "copied 5 characters") || !strings.Contains(status, "goat "+clock) {
		t.Fatalf("status with message %q", status)
	}
	// Off: no directory.
	m.cfg.StatusShowDir = false
	if strings.Contains(render(m)[11], "nontechno") {
		t.Fatal("directory shown with status_show_dir off")
	}
}

// A new window's shell starts in the active window's current directory.
func TestNewWindowStartsInActiveDir(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc")
	}
	launch, other := t.TempDir(), t.TempDir()
	cfg := DefaultConfig()
	cfg.Shell = "/bin/sh"
	m := newWM(cfg, nil, 100, 30)
	m.dir = launch
	done := make(chan struct{})
	defer close(done)
	go func() { // discard program output
		for {
			select {
			case <-m.out:
			case <-done:
				return
			}
		}
	}()
	defer m.closeAll()

	waitDir := func(w *Window, want string) {
		t.Helper()
		want, _ = filepath.EvalSymlinks(want)
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if w.currentDir() == want {
				return
			}
		}
		t.Fatalf("window %d in %q, want %q", w.id, w.currentDir(), want)
	}

	if err := m.newWindow(); err != nil {
		t.Fatal(err)
	}
	w1 := m.focused
	waitDir(w1, launch)
	w1.send([]byte("cd " + other + "\n"))
	waitDir(w1, other)

	if err := m.newWindow(); err != nil {
		t.Fatal(err)
	}
	w2 := m.focused
	if w2 == w1 {
		t.Fatal("no new window")
	}
	waitDir(w2, other)

	// The active window's directory is gone: fall back to the launch folder.
	gone := filepath.Join(other, "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	w2.send([]byte("cd " + gone + "\n"))
	waitDir(w2, gone)
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if err := m.newWindow(); err != nil {
		t.Fatal(err)
	}
	waitDir(m.focused, launch)
}

// The clock has its own colors, so it stands apart from the directory.
func TestClockHighlighted(t *testing.T) {
	m, _, _, b := wmWithBackground(t)
	m.cfg.StatusShowDir = true
	b.dir = "/srv/data"
	scr := uv.NewScreen(m.cols, m.rows)
	scene{m}.Draw(scr, scr.Bounds())
	y := m.rows - 1
	clockX := m.cols - len(" 15:04 ")
	c := scr.CellAt(clockX+1, y) // first digit
	if c == nil || c.Style.Bg != m.cfg.Theme.ClockBg.C || c.Style.Fg != m.cfg.Theme.ClockFg.C {
		t.Fatalf("clock cell %+v: want clock colors", c)
	}
	d := scr.CellAt(clockX-1, y) // last letter of the directory
	if d == nil || d.Content != "a" || d.Style.Bg != m.cfg.Theme.StatusBg.C {
		t.Fatalf("directory cell %+v: want status bar colors", d)
	}
}
