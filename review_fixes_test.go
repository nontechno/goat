package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/creack/pty"
)

// Closing the PTY master must end a pending Read, or a closed window's
// reader goroutine lives on while anything else keeps the terminal open.
func TestClosedPTYEndsRead(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skip(err)
	}
	defer tty.Close() // stands for a job that keeps the terminal open
	ptmx = pollable(ptmx)
	setWinsize(ptmx, 90, 20) // ioctls must not make it blocking again
	if r, c, err := pty.Getsize(tty); err != nil || c != 90 || r != 20 {
		t.Fatalf("size %dx%d %v", c, r, err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ptmx.Read(make([]byte, 64))
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_ = ptmx.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Read still blocked after Close")
	}
}

// Toggling wrap back on with a word selected beyond the new width used to
// index past the line (a crash).
func TestWrapToggleWithWordSelection(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 40, 12)
	w := fakeFramed(1, 0, 0, 30, 8, true, frameCompact)
	w.nowrapW = 512
	m.windows = []*Window{w}
	m.focus(w)
	alt := func(r rune) { _ = m.handleKey(uv.Key{Code: r, Text: string(r), Mod: uv.ModAlt}) }
	alt('z')
	_, _ = w.emu.Write([]byte(strings.Repeat("word ", 80)))
	for range 10 {
		alt('>')
	}
	x, y := w.contentX()+3, w.contentY()
	for range 2 { // double-click: select a word far to the right
		m.handleMouse(uv.MouseClickEvent{X: x, Y: y, Button: uv.MouseLeft})
		m.handleMouse(uv.MouseReleaseEvent{X: x, Y: y, Button: uv.MouseLeft})
	}
	alt('z')
	_ = render(m) // must not panic
}

// Once the history is full, every new line drops the oldest; a view
// scrolled back and a selection must still stay on their text.
func TestScrolledViewStaysPutWhenHistoryFull(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 40, 12)
	w := fakeFramed(1, 0, 0, 30, 8, true, frameCompact) // 6 content rows
	w.emu.SetScrollbackSize(20)
	m.windows = []*Window{w}
	m.focus(w)
	for i := range 50 {
		w.feed(fmt.Appendf(nil, "line %d\r\n", i))
	}
	m.scrollWindow(w, 5)
	top := func() string { return strings.TrimSpace(strings.Trim(render(m)[w.contentY()], "│ ")) }
	before := top()

	// Select that top line.
	y := w.contentY()
	m.handleMouse(uv.MouseClickEvent{X: w.contentX(), Y: y, Button: uv.MouseLeft})
	m.handleMouse(uv.MouseMotionEvent{X: w.contentX() + 6, Y: y, Button: uv.MouseLeft})
	m.handleMouse(uv.MouseReleaseEvent{X: w.contentX() + 6, Y: y, Button: uv.MouseLeft})

	w.feed([]byte("line 50\r\nline 51\r\n"))
	if got := top(); got != before {
		t.Fatalf("view moved: top %q, was %q", got, before)
	}
	var copied string
	copied = m.sel.text()
	if copied != before {
		t.Fatalf("selection moved: %q, want %q", copied, before)
	}
}

func TestScrollbackZeroKeepsNone(t *testing.T) {
	w := fakeFramed(1, 0, 0, 30, 8, true, frameCompact)
	w.emu.SetScrollbackSize(0)
	for i := range 30 {
		w.feed(fmt.Appendf(nil, "line %d\r\n", i))
	}
	if n := w.emu.ScrollbackLen(); n != 0 {
		t.Fatalf("scrollback_lines = 0 kept %d lines", n)
	}
}

// A key typed while scrolled back returns to the live view, which must be
// redrawn even if the key produces no output (a password prompt).
func TestTypingWhileScrolledRedraws(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 40, 12)
	w := fakeFramed(1, 0, 0, 30, 8, true, frameCompact)
	m.windows = []*Window{w}
	m.focus(w)
	for i := range 30 {
		w.feed(fmt.Appendf(nil, "line %d\r\n", i))
	}
	m.scrollWindow(w, 5)
	m.dirty = false
	_ = m.handleKey(uv.Key{Code: 'a', Text: "a"})
	if w.scroll != 0 || !m.dirty {
		t.Fatalf("scroll=%d dirty=%v", w.scroll, m.dirty)
	}
}

func TestBlankShell(t *testing.T) {
	if _, err := newWindow(1, "   ", 0, 0, 20, 5, winOpts{}, make(chan ptyMsg, 1)); err == nil {
		t.Fatal("a blank shell should be an error, not a crash")
	}
	m := newWM(DefaultConfig(), nil, 40, 12)
	m.cfg.Shell = "  "
	if s := m.shell(); strings.TrimSpace(s) == "" {
		t.Fatalf("shell() = %q", s)
	}
}
