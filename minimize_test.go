package main

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// statusLine is the status bar as drawn.
func statusLine(m *WM) string {
	scr := uv.NewScreen(m.cols, m.rows)
	scene{m}.Draw(scr, scr.Bounds())
	var b strings.Builder
	for x := 0; x < m.cols; x++ {
		if c := scr.CellAt(x, m.rows-1); c != nil && !c.IsZero() {
			b.WriteString(c.Content)
		}
	}
	return b.String()
}

func TestAlt0HidesAndRestores(t *testing.T) {
	m, bg, a, b := wmWithBackground(t)
	alt := func(r rune) {
		t.Helper()
		if err := m.handleKey(uv.Key{Code: r, Mod: uv.ModAlt}); err != nil {
			t.Fatal(err)
		}
	}
	m.toggleEvents() // shown, and stays so
	ev := m.ev.win

	alt('0') // focus the background
	if m.focused != bg || a.minimized || b.minimized {
		t.Fatalf("first Alt+0: focused %v, hidden %v %v", m.focused.id, a.minimized, b.minimized)
	}
	alt('0') // hide the others
	if !a.minimized || !b.minimized || ev.minimized || m.focused != bg {
		t.Fatalf("second Alt+0: hidden %v %v, events %v", a.minimized, b.minimized, ev.minimized)
	}
	if !m.eventsShown() {
		t.Fatal("the events window went away")
	}
	line := statusLine(m)
	if !strings.Contains(line, ghost+"1:") || !strings.Contains(line, ghost+"2:") || strings.Contains(line, ghost+"events") {
		t.Fatalf("status bar %q", line)
	}
	// Not drawn, not clickable: the background (or the events window) is
	// what's there.
	if w := m.topAt(a.x+1, a.y+1); w == a || w == b {
		t.Fatalf("a hidden window is clickable")
	}
	// Alt+n / Alt+p skip hidden windows.
	alt(rune(m.cfg.Keys.FocusNext[0]))
	if m.focused.minimized {
		t.Fatal("Alt+n focused a hidden window")
	}

	// Alt+1 brings window 1 back alone.
	alt('1')
	if a.minimized || !b.minimized || m.focused != a {
		t.Fatalf("Alt+1: a hidden %v, b hidden %v, focused %d", a.minimized, b.minimized, m.focused.id)
	}
	// Alt+0 twice: focus the background, then bring back the rest.
	alt('0')
	alt('0')
	if a.minimized || b.minimized || m.focused != bg {
		t.Fatalf("restore: %v %v", a.minimized, b.minimized)
	}
	if strings.Contains(statusLine(m), ghost) {
		t.Fatal("ghosts left in the status bar")
	}

	// A click on a hidden window's tab brings it back.
	alt('0') // hide again (the background has the focus)
	if !a.minimized || !b.minimized {
		t.Fatal("not hidden again")
	}
	statusLine(m) // draws the tabs
	for _, tab := range m.tabs {
		if tab.win == b {
			m.handleMouse(uv.MouseClickEvent{X: tab.x0 + 1, Y: m.rows - 1, Button: uv.MouseLeft})
		}
	}
	if b.minimized || !a.minimized || m.focused != b {
		t.Fatalf("tab click: b hidden %v, a hidden %v", b.minimized, a.minimized)
	}
}

// Closing the focused window never hands the focus to a hidden one.
func TestCloseSkipsHidden(t *testing.T) {
	m, bg, a, b := wmWithBackground(t)
	m.hide(a)
	m.focus(b)
	m.remove(b)
	if m.focused == a || !a.minimized {
		t.Fatalf("focus went to the hidden window")
	}
	if m.focused != bg {
		t.Fatalf("focused %v", m.focused)
	}
}
