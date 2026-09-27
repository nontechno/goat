package main

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestFrameConfig(t *testing.T) {
	for in, want := range map[string]frameStyle{
		``:                                 frameFull,
		`frame = "full"`:                   frameFull,
		`frame = "compact"`:                frameCompact,
		`frame = "none"`:                   frameNone,
		`compact = true`:                   frameCompact, // older form
		`compact = false`:                  frameFull,
		"frame = \"none\"\ncompact = true": frameNone, // frame wins
	} {
		c, warns := parseConfig(in + "\n")
		if len(warns) != 0 || c.frame != want {
			t.Errorf("%q: frame %v warns %v", in, c.frame, warns)
		}
	}
	c, warns := parseConfig(`frame = "thin"` + "\n")
	if len(warns) != 1 || !strings.Contains(warns[0], "frame") || c.frame != frameFull {
		t.Fatalf("bad frame: %v %v", c.frame, warns)
	}
}

func TestFrameNoneGeometry(t *testing.T) {
	w := fakeFramed(1, 10, 5, 20, 8, true, frameNone)
	if w.contentX() != 10 || w.contentY() != 6 || w.contentW() != 20 || w.contentH() != 7 {
		t.Fatalf("content %d,%d %dx%d", w.contentX(), w.contentY(), w.contentW(), w.contentH())
	}
	if w.onLeft(10, 8) || w.onRight(29, 8) || w.onBottom(15, 12) {
		t.Fatal("frame none has no side or bottom edges")
	}
	if w.onHandle(10, 5) != -1 || w.onHandle(29, 5) != 1 || w.onHandle(15, 5) != 0 || w.onHandle(10, 6) != 0 {
		t.Fatal("handles")
	}
	if !w.inTitleBar(15, 5) || w.inTitleBar(15, 6) {
		t.Fatal("title bar")
	}
	cfg := DefaultConfig()
	cfg.frame = frameNone
	cfg.Layout.MinWindowRows = 1
	if m := newWM(cfg, nil, 80, 24); m.minH() != 2 {
		t.Fatalf("minH %d", m.minH())
	}
}

func TestDrawFrameNone(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 16, 5)
	w := fakeFramed(1, 0, 0, 13, 3, true, frameNone)
	_, _ = w.emu.WriteString("hello")
	m.windows, m.focused = []*Window{w}, w
	got := render(m)
	for i, want := range []string{"── 1:bash ───   ", "hello           ", "                "} {
		if got[i] != want {
			t.Errorf("row %d: %q want %q", i, got[i], want)
		}
	}
}

func TestFrameNoneResizeByHandles(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 80, 24)
	m.cfg.frame = frameNone
	w := fakeFramed(1, 10, 5, 20, 8, true, frameNone)
	m.windows, m.focused = []*Window{w}, w
	drag := func(x0, y0, x1, y1 int) {
		m.handleMouse(uv.MouseClickEvent{X: x0, Y: y0, Button: uv.MouseLeft})
		m.handleMouse(uv.MouseMotionEvent{X: x1, Y: y1, Button: uv.MouseLeft})
		m.handleMouse(uv.MouseReleaseEvent{X: x1, Y: y1, Button: uv.MouseLeft})
	}
	drag(29, 5, 34, 8) // right end: +5 wide, +3 tall
	if w.x != 10 || w.y != 5 || w.w != 25 || w.h != 11 {
		t.Fatalf("right handle: %d,%d %dx%d", w.x, w.y, w.w, w.h)
	}
	drag(10, 5, 6, 3) // left end: 4 wider to the left, 2 shorter
	if w.x != 6 || w.y != 5 || w.w != 29 || w.h != 9 {
		t.Fatalf("left handle: %d,%d %dx%d", w.x, w.y, w.w, w.h)
	}
	drag(15, 5, 18, 7) // middle of the line moves the window
	if w.x != 9 || w.y != 7 || w.w != 29 {
		t.Fatalf("move: %d,%d %dx%d", w.x, w.y, w.w, w.h)
	}
}

func TestBoxResizeIsRelative(t *testing.T) {
	// Grabbing a border one cell in from where the pointer lands must not
	// make the window jump.
	m := newWM(DefaultConfig(), nil, 80, 24)
	w := fakeWindow(1, 10, 5, 20, 10, true, false)
	m.windows, m.focused = []*Window{w}, w
	m.handleMouse(uv.MouseClickEvent{X: 29, Y: 14, Button: uv.MouseLeft}) // bottom-right corner
	m.handleMouse(uv.MouseMotionEvent{X: 31, Y: 15, Button: uv.MouseLeft})
	if w.w != 22 || w.h != 11 {
		t.Fatalf("corner: %dx%d", w.w, w.h)
	}
}
