package main

import (
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// fakeWindow builds a window without a process, for layout and drawing tests.
func fakeWindow(id, x, y, w, h int, decorated, compact bool) *Window {
	f := frameFull
	if compact {
		f = frameCompact
	}
	return fakeFramed(id, x, y, w, h, decorated, f)
}

func fakeFramed(id, x, y, w, h int, decorated bool, f frameStyle) *Window {
	win := &Window{id: id, x: x, y: y, w: w, h: h, decorated: decorated, frame: f,
		shellName: "bash", cursorVisible: true, in: newInputQueue()}
	win.emu = vt.NewEmulator(win.contentW(), win.contentH())
	return win
}

func ids(ws []*Window) []int {
	var out []int
	for _, w := range ws {
		out = append(out, w.id)
	}
	return out
}

func TestRaiseRespectsPins(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 80, 24)
	a, b, c := fakeWindow(1, 0, 0, 10, 5, true, false), fakeWindow(2, 0, 0, 10, 5, true, false), fakeWindow(3, 0, 0, 10, 5, true, false)
	m.windows = []*Window{a, b, c}

	m.raise(a) // unpinned, no pins: to the top
	if got := ids(m.windows); !slices.Equal(got, []int{2, 3, 1}) {
		t.Fatalf("raise a: %v", got)
	}
	b.pinned = true
	m.raise(b) // pinned: very top
	if got := ids(m.windows); !slices.Equal(got, []int{3, 1, 2}) {
		t.Fatalf("pin b: %v", got)
	}
	m.raise(c) // unpinned: just below the first pinned window
	if got := ids(m.windows); !slices.Equal(got, []int{1, 3, 2}) {
		t.Fatalf("raise c: %v", got)
	}
	a.pinned = true
	m.raise(a) // second pin goes above the first
	if got := ids(m.windows); !slices.Equal(got, []int{3, 2, 1}) {
		t.Fatalf("pin a: %v", got)
	}
}

func TestGeometry(t *testing.T) {
	full := fakeWindow(1, 10, 5, 20, 10, true, false)
	compact := fakeWindow(2, 10, 5, 20, 10, true, true)
	plain := fakeWindow(3, 10, 5, 20, 10, false, false)
	for _, c := range []struct {
		w          *Window
		cy, ch, cw int
	}{
		{full, 8, 6, 18},
		{compact, 6, 8, 18},
		{plain, 6, 8, 18},
	} {
		if c.w.contentY() != c.cy || c.w.contentH() != c.ch || c.w.contentW() != c.cw || c.w.contentX() != 11 {
			t.Errorf("window %d: content (%d,%d %dx%d)", c.w.id, c.w.contentX(), c.w.contentY(), c.w.contentW(), c.w.contentH())
		}
	}
	if !full.inTitleBar(12, 6) || compact.inTitleBar(12, 6) || !compact.inTitleBar(12, 5) {
		t.Error("title bar hit test wrong")
	}
	if !full.onLeft(10, 9) || !full.onRight(29, 9) || !full.onBottom(15, 14) || full.onBottom(15, 13) {
		t.Error("edge hit tests wrong")
	}
}

func TestPlaceClampsAndKeepsReachable(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 80, 24)
	w := fakeWindow(1, 0, 0, 20, 10, true, false)
	m.windows = []*Window{w}
	m.place(w, -100, -5, 2, 2) // too small, off the top/left
	if w.w != 6 || w.h != 5 || w.x != -5 || w.y != 0 {
		t.Fatalf("got %d,%d %dx%d", w.x, w.y, w.w, w.h)
	}
	if w.emu.Width() != w.contentW() || w.emu.Height() != w.contentH() {
		t.Fatalf("emulator not resized: %dx%d", w.emu.Width(), w.emu.Height())
	}
	m.resizeScreen(30, 10)
	m.place(w, 200, 200, 20, 10)
	if w.x != 29 || w.y != 8 { // row 9 is the status bar
		t.Fatalf("not clamped to screen: %d,%d", w.x, w.y)
	}
}

func TestMinHeightFollowsTitleStyle(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Layout.MinWindowRows = 3
	m := newWM(cfg, nil, 80, 24)
	if m.minH() != 5 { // full title box needs 2 borders + 2 title rows + 1 line
		t.Fatalf("full minH = %d", m.minH())
	}
	cfg.frame = frameCompact
	if m.minH() != 3 {
		t.Fatalf("compact minH = %d", m.minH())
	}
}

func TestNumbersAreStable(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 80, 24)
	a, b, c := fakeWindow(4, 0, 0, 10, 5, true, false), fakeWindow(9, 0, 0, 10, 5, true, false), fakeWindow(7, 0, 0, 10, 5, true, false)
	m.windows = []*Window{b, a, c} // z-order differs from creation order
	if m.number(a) != 1 || m.number(c) != 2 || m.number(b) != 3 {
		t.Fatalf("numbers: a=%d c=%d b=%d", m.number(a), m.number(c), m.number(b))
	}
}

func render(m *WM) []string {
	scr := uv.NewScreen(m.cols, m.rows)
	scr.Clear()
	scene{m}.Draw(scr, scr.Bounds())
	var out []string
	for y := range m.rows {
		var sb strings.Builder
		for x := 0; x < m.cols; {
			c := scr.CellAt(x, y)
			if c == nil || c.Content == "" {
				sb.WriteByte(' ')
				x++
				continue
			}
			sb.WriteString(c.Content)
			x += max(c.Width, 1)
		}
		out = append(out, sb.String())
	}
	return out
}

func TestDrawFrames(t *testing.T) {
	cfg := DefaultConfig()
	m := newWM(cfg, nil, 16, 6)
	w := fakeWindow(1, 0, 0, 13, 5, true, false)
	m.windows = []*Window{w}
	m.focused = w
	got := render(m)
	want := []string{
		"╭───────────╮   ",
		"│  1:bash   │   ",
		"├───────────┤   ",
		"│           │   ",
		"└──── wrap ─┘   ", // the wrap indicator
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("full row %d: %q want %q", i, got[i], want[i])
		}
	}

	cfg.frame = frameCompact
	w = fakeWindow(1, 0, 0, 13, 3, true, true)
	m.windows, m.focused = []*Window{w}, w
	got = render(m)
	for i, s := range []string{"╭─ 1:bash ──╮   ", "│           │   ", "└──── wrap ─┘   "} {
		if got[i] != s {
			t.Errorf("compact row %d: %q want %q", i, got[i], s)
		}
	}
}

func TestCompactBorderBackground(t *testing.T) {
	cfg := DefaultConfig()
	cfg.frame = frameCompact
	bg := idx(236)
	cfg.Theme.CompactTitleBg = &bg
	m := newWM(cfg, nil, 20, 5)
	w := fakeWindow(1, 1, 1, 12, 3, true, true)
	m.windows, m.focused = []*Window{w}, w
	scr := uv.NewScreen(20, 5)
	scene{m}.Draw(scr, scr.Bounds())
	// Window at x=1..12, y=1..3: every border cell gets the background.
	for x := 1; x <= 12; x++ {
		for _, y := range []int{1, 3} { // top (title) and bottom rows
			if c := scr.CellAt(x, y); c.Style.Bg != bg.C {
				t.Fatalf("border x=%d y=%d bg %v", x, y, c.Style.Bg)
			}
		}
	}
	for _, x := range []int{1, 12} { // left and right sides
		if c := scr.CellAt(x, 2); c.Style.Bg != bg.C {
			t.Fatalf("side x=%d bg %v", x, c.Style.Bg)
		}
	}
	for x := 2; x < 12; x++ { // content keeps the terminal default
		if c := scr.CellAt(x, 2); c.Style.Bg != nil {
			t.Fatalf("content x=%d bg %v", x, c.Style.Bg)
		}
	}
}

func TestContentWideCharsAndClipping(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 12, 4)
	w := fakeWindow(1, -1, 0, 10, 3, false, false) // left border off screen
	_, _ = w.emu.WriteString("a日b")
	m.windows, m.focused = []*Window{w}, w
	got := render(m)
	if got[1] != "a日b    │   " {
		t.Fatalf("row: %q", got[1])
	}

	// A wide character cut by the screen's left edge becomes a blank.
	w2 := fakeWindow(2, -2, 0, 10, 3, false, false) // content starts at x=-1
	_, _ = w2.emu.WriteString("日b")
	m.windows, m.focused = []*Window{w2}, w2
	if got := render(m); got[1] != " b     │    " {
		t.Fatalf("clipped row: %q", got[1])
	}
}

func TestCursorStyleFromProgram(t *testing.T) {
	// The callback wiring lives in newWindow; replicate it on a fake window.
	w := fakeWindow(1, 0, 0, 10, 5, false, false)
	w.cursorBlink = true // as newWindow starts it
	w.emu.SetCallbacks(vt.Callbacks{CursorStyle: func(s vt.CursorStyle, steady bool) {
		w.cursorShape, w.cursorBlink = uv.CursorShape(s), !steady
	}})
	for _, c := range []struct {
		seq   string
		shape uv.CursorShape
		blink bool
	}{
		{"\x1b[5 q", uv.CursorBar, true},
		{"\x1b[6 q", uv.CursorBar, false},
		{"\x1b[2 q", uv.CursorBlock, false},
		{"\x1b[0 q", uv.CursorBlock, true},
	} {
		seq, want := c.seq, c
		_, _ = w.emu.WriteString(seq)
		if w.cursorShape != want.shape || w.cursorBlink != want.blink {
			t.Errorf("%q: got %v blink=%v", seq, w.cursorShape, w.cursorBlink)
		}
	}
}
