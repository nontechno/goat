package main

import (
	"bytes"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestHostPointers(t *testing.T) {
	for _, tc := range []struct {
		setting string
		env     []string
		want    *pointerNames
	}{
		{"auto", []string{"TERM=xterm-256color", "TERM_PROGRAM=Apple_Terminal"}, nil},
		{"auto", []string{"TERM=xterm-256color", "TERM_PROGRAM=iTerm.app"}, &x11Pointers},
		{"auto", []string{"TERM=xterm-256color", "LC_TERMINAL=iTerm2"}, &x11Pointers}, // over ssh
		{"auto", []string{"TERM=xterm-kitty"}, &cssPointers},
		{"auto", []string{"TERM=xterm-ghostty"}, &cssPointers},
		{"auto", []string{"TERM=tmux-256color", "TERM_PROGRAM=iTerm.app"}, nil},
		{"off", []string{"TERM=xterm-kitty"}, nil},
		{"on", []string{"TERM=xterm-256color"}, &cssPointers},
	} {
		if got, why := hostPointers(tc.setting, tc.env); got != tc.want {
			t.Errorf("%s %v: %v (%s)", tc.setting, tc.env, got, why)
		}
	}
}

// The pointer follows what is under it: text, edges, corners, a program
// that uses the mouse, the title bar, a drag going on.
func TestPointerAt(t *testing.T) {
	m, _, a, _ := wmWithBackground(t)
	m.pointerOn = true
	m.focus(a)
	cx, cy := a.contentX()+1, a.contentY()+1
	for _, tc := range []struct {
		name string
		x, y int
		want pointerShape
	}{
		{"text", cx, cy, ptrText},
		{"title", a.x + 3, a.y, ptrDefault},
		{"left edge", a.x, cy, ptrEW},
		{"right edge", a.x + a.w - 1, cy, ptrEW},
		{"bottom", a.x + 3, a.y + a.h - 1, ptrNS},
		{"bottom right", a.x + a.w - 1, a.y + a.h - 1, ptrNWSE},
		{"bottom left", a.x, a.y + a.h - 1, ptrNESW},
		{"status bar", 1, m.rows - 1, ptrDefault},
	} {
		if got := m.pointerAt(tc.x, tc.y); got != tc.want {
			t.Errorf("%s (%d,%d): %v, want %v", tc.name, tc.x, tc.y, got, tc.want)
		}
	}
	a.mouseModes = map[ansi.DECMode]bool{1000: true} // a program that uses the mouse
	if got := m.pointerAt(cx, cy); got != ptrDefault {
		t.Errorf("program with the mouse: %v", got)
	}
	a.mouseModes[1000] = false
	// Moving: the move pointer, wherever the mouse is.
	m.handleMouse(uv.MouseClickEvent{X: a.x + 3, Y: a.y, Button: uv.MouseLeft})
	m.handleMouse(uv.MouseMotionEvent{X: a.x + 6, Y: a.y + 2, Button: uv.MouseLeft})
	if m.pointer != ptrMove {
		t.Errorf("moving: %v", m.pointer)
	}
	m.handleMouse(uv.MouseReleaseEvent{X: a.x + 6, Y: a.y + 2, Button: uv.MouseLeft})
	if m.pointer != ptrDefault { // over the title bar again
		t.Errorf("after the move: %v", m.pointer)
	}
	// Hovering (no button): the pointer changes with what is under it.
	m.handleMouse(uv.MouseMotionEvent{X: a.contentX() + 1, Y: a.contentY() + 1})
	if m.pointer != ptrText {
		t.Errorf("hover over text: %v", m.pointer)
	}
}

// The terminal is told the shape once, when it changes; and the text beam
// back when goat ends. Every movement is reported (mode 1003).
func TestPointerWritten(t *testing.T) {
	var out bytes.Buffer
	s := newHostScreen(&out, nil, 20, 5)
	s.pointers = &cssPointers
	d := uv.DrawableFunc(func(uv.Screen, uv.Rectangle) {})
	for _, p := range []pointerShape{ptrText, ptrText, ptrEW} {
		if err := s.render(d, cursorState{pointer: p}); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(out.String(), "\x1b]22;"); got != 2 || !strings.Contains(out.String(), "\x1b]22;ew-resize\x1b\\") {
		t.Errorf("%d OSC 22 in %q", got, out.String())
	}
	out.Reset()
	_ = s.setup(true)
	if !strings.Contains(out.String(), "\x1b[?1003h") {
		t.Errorf("no any-event mouse mode: %q", out.String())
	}
	out.Reset()
	s.teardown(true)
	if !strings.Contains(out.String(), "\x1b]22;text\x1b\\") {
		t.Errorf("teardown: %q", out.String())
	}
}

// From the start the pointer is the arrow (both names to a terminal not
// known for OSC 22; nothing with pointer_shapes = "off"); the terminal's
// history is emptied, before the alternate screen, only when asked.
func TestPointerAndScrollbackAtStart(t *testing.T) {
	var out bytes.Buffer
	s := newHostScreen(&out, nil, 20, 5)
	s.pointers = &x11Pointers
	_ = s.setup(true)
	if got := out.String(); !strings.Contains(got, "\x1b]22;left_ptr\x1b\\") || strings.Contains(got, "\x1b[3J") {
		t.Errorf("known terminal: %q", got)
	}
	out.Reset()
	s = newHostScreen(&out, nil, 20, 5)
	s.clearScrollback = true
	_ = s.setup(true)
	got := out.String()
	if !strings.Contains(got, "\x1b]22;default\x1b\\\x1b]22;left_ptr\x1b\\") {
		t.Errorf("unknown terminal: %q", got)
	}
	if i, j := strings.Index(got, "\x1b[3J"), strings.Index(got, "\x1b[?1049h"); i < 0 || i > j {
		t.Errorf("history not emptied before the alternate screen: %q", got)
	}
	out.Reset()
	s.teardown(true)
	if !strings.Contains(out.String(), "\x1b]22;text\x1b\\\x1b]22;xterm\x1b\\") {
		t.Errorf("teardown: %q", out.String())
	}
	out.Reset()
	s = newHostScreen(&out, nil, 20, 5)
	s.pointerOff = true
	_ = s.setup(true)
	s.teardown(true)
	if strings.Contains(out.String(), "\x1b]22;") {
		t.Errorf("off: %q", out.String())
	}
}
