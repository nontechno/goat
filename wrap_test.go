package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestNoWrap(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 40, 12)
	w := fakeFramed(1, 0, 0, 30, 8, true, frameCompact) // content 28x6
	w.nowrapW = m.cfg.NowrapWidth
	m.windows = []*Window{w}
	m.focus(w)
	cw := w.contentW()
	long := strings.Repeat("0123456789", 10) // 100 columns
	alt := func(r rune) {
		t.Helper()
		if err := m.handleKey(uv.Key{Code: r, Text: string(r), Mod: uv.ModAlt}); err != nil {
			t.Fatal(err)
		}
	}
	bottom := func() string { return render(m)[w.y+w.h-1] }
	row := func(r int) string { return render(m)[w.contentY()+r] }

	if !strings.Contains(bottom(), wrapSymbol) {
		t.Fatalf("indicator: %q", bottom())
	}

	alt('z') // no wrap
	if !w.nowrap || w.emu.Width() != 512 {
		t.Fatalf("toggle: nowrap=%v width=%d", w.nowrap, w.emu.Width())
	}
	if !strings.Contains(bottom(), nowrapSymbol) {
		t.Fatalf("indicator: %q", bottom())
	}
	_, _ = w.emu.Write([]byte(long + "\r\nnext"))
	if got := strings.TrimSpace(row(0)); got != "│"+long[:cw]+"│" && !strings.HasPrefix(got, "│"+long[:cw]) {
		t.Fatalf("row 0 %q", got)
	}
	if !strings.Contains(row(1), "next") {
		t.Fatalf("the long line wrapped: row 1 %q", row(1))
	}

	alt('>') // view right by half a window
	if w.hscroll != cw/2 {
		t.Fatalf("Alt+>: hscroll %d", w.hscroll)
	}
	if !strings.Contains(row(0), long[cw/2:cw/2+cw]) {
		t.Fatalf("scrolled row 0 %q", row(0))
	}
	if b := bottom(); !strings.Contains(b, nowrapSymbol+"─┘") || strings.Contains(b, "+") {
		t.Fatalf("indicator when scrolled sideways (no offset shown): %q", b)
	}

	// Copying from a scrolled view takes the text under the pointer.
	var copied string
	m.copy = func(text, _ string) { copied = text }
	y := w.contentY()
	m.handleMouse(uv.MouseClickEvent{X: w.contentX(), Y: y, Button: uv.MouseLeft})
	m.handleMouse(uv.MouseMotionEvent{X: w.contentX() + 4, Y: y, Button: uv.MouseLeft})
	m.handleMouse(uv.MouseReleaseEvent{X: w.contentX() + 4, Y: y, Button: uv.MouseLeft})
	if want := long[cw/2 : cw/2+5]; copied != want {
		t.Fatalf("copied %q, want %q", copied, want)
	}

	// Shift+wheel scrolls sideways; Alt+< back to the start.
	m.handleMouse(uv.MouseWheelEvent{X: 5, Y: 3, Button: uv.MouseWheelDown, Mod: uv.ModShift})
	if w.hscroll != cw/2+8 {
		t.Fatalf("Shift+wheel: hscroll %d", w.hscroll)
	}
	alt('<')
	alt('<')
	alt('<')
	if w.hscroll != 0 {
		t.Fatalf("Alt+<: hscroll %d", w.hscroll)
	}

	// Typing follows the cursor: output past the edge brings it into view.
	w.followCursor = true
	_, _ = w.emu.Write([]byte("\r\n" + long))
	if !w.followCursorView() || w.hscroll == 0 {
		t.Fatal("view did not follow the cursor")
	}
	if x, _, ok := m.cursorFor(); !ok || x < w.contentX() || x >= w.contentX()+cw {
		t.Fatalf("cursor not in view: %d %v", x, ok)
	}

	alt('z') // wrap again
	if w.nowrap || w.emu.Width() != cw || w.hscroll != 0 {
		t.Fatalf("back to wrap: nowrap=%v width=%d hscroll=%d", w.nowrap, w.emu.Width(), w.hscroll)
	}
	if w.scrollSideways(5) {
		t.Fatal("sideways scroll while wrapping")
	}
}

func TestWrapConfig(t *testing.T) {
	c, warns := parseConfig("wrap_lines = false\nnowrap_width = 10\n")
	if c.WrapLines || c.NowrapWidth != 512 || len(warns) != 1 {
		t.Fatalf("wrap_lines=%v nowrap_width=%d warns=%v", c.WrapLines, c.NowrapWidth, warns)
	}
	if !DefaultConfig().WrapLines {
		t.Fatal("wrapping should be the default")
	}
}

// A new window wraps as the window it is opened from does; with none (or
// the events window) active, as wrap_lines says.
func TestNewWindowInheritsWrap(t *testing.T) {
	m, bg, a, b := wmWithBackground(t)
	a.nowrap, b.nowrap = true, false
	m.focus(a)
	if m.newWindowWrap() {
		t.Error("from a non-wrapping window: wraps")
	}
	m.focus(b)
	if !m.newWindowWrap() {
		t.Error("from a wrapping window: doesn't wrap")
	}
	bg.nowrap = true
	m.focus(bg)
	if m.newWindowWrap() {
		t.Error("from the non-wrapping background: wraps")
	}
	m.cfg.WrapLines = false
	m.toggleEvents() // the events window is focused: wrap_lines decides
	bg.nowrap = false
	if m.newWindowWrap() {
		t.Error("from the events window: not wrap_lines")
	}
	m.focus(nil)
	m.cfg.WrapLines = true
	if !m.newWindowWrap() {
		t.Error("no window: not wrap_lines")
	}
}

func TestLocalWrapLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.toml")
	for file, want := range map[string]string{
		"":                     "unset",
		"wrap_lines = false\n": "false",
		"wrap_lines = true\n":  "true",
		"hostname = \"x\"\n[background_window]\nwrap_lines = false\n": "unset", // in a section: not this key
	} {
		_ = os.WriteFile(path, []byte(file), 0o644)
		lc, _ := readLocalConfig(path)
		got := "unset"
		if lc.WrapLines != nil {
			got = fmt.Sprint(*lc.WrapLines)
		}
		if got != want {
			t.Errorf("%q: %s, want %s", file, got, want)
		}
	}
}
