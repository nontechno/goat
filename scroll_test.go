package main

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestScrollThumb(t *testing.T) {
	for _, c := range []struct{ h, sb, scroll, pos, size int }{
		{10, 90, 90, 0, 1}, // oldest: top
		{10, 90, 1, 8, 1},  // one line back: near the bottom
		{10, 10, 10, 0, 5}, // half the lines visible: half-height thumb
		{10, 10, 5, 2, 5},  // halfway
		{4, 1000, 500, 1, 1},
	} {
		pos, size := scrollThumb(c.h, c.sb, c.scroll)
		if pos != c.pos || size != c.size {
			t.Errorf("scrollThumb(%d,%d,%d) = %d,%d want %d,%d", c.h, c.sb, c.scroll, pos, size, c.pos, c.size)
		}
		if pos < 0 || pos+size > c.h {
			t.Errorf("thumb outside the window: %d+%d of %d", pos, size, c.h)
		}
	}
}

func TestScrollKeysAndMarker(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 60, 20)
	w := fakeFramed(1, 2, 1, 30, 12, true, frameCompact) // content 28x10
	m.windows = []*Window{w}
	m.focus(w)
	for i := range 50 {
		fmt.Fprintf(w.emu, "line %d\r\n", i)
	}
	sb := w.emu.ScrollbackLen()
	if sb < 30 {
		t.Fatalf("scrollback %d", sb)
	}
	key := func(code rune, mod uv.KeyMod) {
		t.Helper()
		if err := m.handleKey(uv.Key{Code: code, Mod: uv.ModAlt | mod}); err != nil {
			t.Fatal(err)
		}
	}
	key(uv.KeyPgUp, uv.ModShift)
	if w.scroll != scrollStep {
		t.Fatalf("Alt+Shift+PageUp: scroll %d, want %d", w.scroll, scrollStep)
	}
	key(uv.KeyPgUp, 0)
	if want := scrollStep + w.contentH() - 1; w.scroll != want {
		t.Fatalf("Alt+PageUp: scroll %d, want %d", w.scroll, want)
	}
	key(uv.KeyPgDown, uv.ModShift)
	if want := w.contentH() - 1; w.scroll != want {
		t.Fatalf("Alt+Shift+PageDown: scroll %d, want %d", w.scroll, want)
	}

	key(uv.KeyHome, 0)
	if w.scroll != sb {
		t.Fatalf("Alt+Home: scroll %d, want %d", w.scroll, sb)
	}
	rows := render(m)
	rightCol := func(row string) string { return string([]rune(row)[w.x+w.w-1]) }
	if got := rightCol(rows[w.contentY()]); got != "┃" {
		t.Fatalf("at the oldest line the marker should be at the top: %q", rows[w.contentY()])
	}
	if got := rightCol(rows[w.contentY()+w.contentH()-1]); got != "│" {
		t.Fatalf("bottom of the border should be plain: %q", got)
	}
	if !strings.Contains(rows[w.contentY()], "line 0") {
		t.Fatalf("oldest line not shown: %q", rows[w.contentY()])
	}

	key(uv.KeyEnd, 0)
	if w.scroll != 0 {
		t.Fatalf("Alt+End: scroll %d", w.scroll)
	}
	for y := w.contentY(); y < w.contentY()+w.contentH(); y++ {
		if strings.Contains(render(m)[y], "┃") {
			t.Fatal("marker shown in the live view")
		}
	}

	// Frame "none": the marker sits in the last content column.
	w.frame = frameNone
	w.emu.Resize(w.contentW(), w.contentH())
	key(uv.KeyHome, 0)
	rows = render(m)
	if got := string([]rune(rows[w.contentY()])[w.contentX()+w.contentW()-1]); got != "▐" {
		t.Fatalf("frame none marker: %q", rows[w.contentY()])
	}
}

// The status bar covers every window, the cursor included.
func TestCursorHiddenUnderStatusBar(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 40, 12)          // status bar on row 11
	w := fakeFramed(1, 0, 5, 30, 10, true, frameNone) // content rows 6..14
	m.windows = []*Window{w}
	m.focus(w)
	_, _ = w.emu.Write([]byte("\x1b[5;3H")) // cursor to content row 4 = screen row 10
	if _, y, ok := m.cursorFor(); !ok || y != 10 {
		t.Fatalf("cursor above the status bar: y=%d ok=%v", y, ok)
	}
	_, _ = w.emu.Write([]byte("\x1b[6;3H")) // content row 5 = screen row 11: the status bar
	if _, y, ok := m.cursorFor(); ok {
		t.Fatalf("cursor shown on the status bar row (y=%d)", y)
	}
}
