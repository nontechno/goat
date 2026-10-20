package main

import (
	"io"
	"slices"
	"strings"
	"time"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// Panels: goat's own windows without a program. They are the events window
// (Alt+-), help (Alt+F1), status (Alt+F2) and inventory (Alt+F3). Each is a regular
// window: move it, resize it, select and copy from it. What sets them
// apart is that they aren't numbered, Alt+0 doesn't hide them, they can't
// be swapped with the background, and nothing is typed or pasted into
// them. Their keys scroll them, and the inventory's choose a window.
// Hidden, a panel keeps its place for the next time it is shown.

// isPanel reports whether w is one of goat's panel windows.
func (m *WM) isPanel(w *Window) bool {
	return m.isEvents(w) || m.isHelp(w) || m.isStatusWin(w) || m.isInventory(w)
}

// panelShown reports whether the panel w is on screen.
func (m *WM) panelShown(w *Window) bool {
	return w != nil && slices.Contains(m.windows, w)
}

// panelPlace is where a panel first opens.
type panelPlace int

const (
	placeCenter panelPlace = iota // most of the width (events, inventory)
	placeRight                    // the right half, full height (help)
	placeLeft                     // top left, narrow (status)
)

// newPanelWindow makes a panel: a frame and an emulator that goat writes
// to, not yet on screen (showPanel).
func (m *WM) newPanelWindow(name string, history int, at panelPlace) *Window {
	area := m.areaH()
	width := min(m.cols, max(min(m.cols-4, 100), m.cols*4/5))
	height := min(area, max(min(area-2, 30), area*3/5))
	x, y := (m.cols-width)/2, (area-height)/2
	switch at {
	case placeRight:
		width = min(m.cols, max(min(m.cols-4, 72), m.cols/2))
		height = area
		x, y = m.cols-width, 0
	case placeLeft:
		width = min(m.cols, max(min(m.cols-4, 66), m.cols/3))
		height = min(area, 30)
		x, y = 0, 0
	}
	w := &Window{
		id: -1, x: x, y: y, w: width, h: height,
		decorated: !m.cfg.DisableMouse, frame: m.cfg.frame,
		shellName: name, nowrapW: m.cfg.NowrapWidth,
		// The events window follows wrap_lines; the others are laid out
		// for their width by goat.
		nowrap:     !m.cfg.WrapLines && at == placeCenter && name == "events",
		mouseModes: map[ansi.DECMode]bool{},
		// no cursor, no input queue, no PTY
	}
	m.placePanel(w, name) // system.toml's size and place, if it says (system.go)
	w.emu = vt.NewEmulator(w.emuWidth(w.contentW()), w.contentH())
	w.emu.SetScrollbackSize(history)
	go func() { _, _ = io.Copy(io.Discard, w.emu) }() // replies go nowhere
	m.initBackground(w)
	m.applyPanelTheme(w, name) // and its palette
	m.applyHostColors(w)
	logger.Info(name + " window opened")
	return w
}

// showPanel puts a panel on screen: on top, focused, where it was last
// time (kept on the screen if that has shrunk).
func (m *WM) showPanel(w *Window) {
	m.applyHostColors(w) // the terminal's colors may have changed while it was hidden
	m.place(w, w.x, w.y, w.w, w.h)
	m.windows = append(m.windows, w)
	m.focus(w)
}

// hidePanel takes a panel window off screen; its place and contents are
// kept for the next time it is shown.
func (m *WM) hidePanel(w *Window) {
	i := slices.Index(m.windows, w)
	if i < 0 {
		return
	}
	m.trackPanelGeometry(time.Now(), true) // a place it was moved to is saved now (system.go)
	if m.isHelp(w) {
		m.cancelKeyEdit()
	}
	m.clearSelectionIn(w)
	m.windows = slices.Delete(m.windows, i, i+1)
	if m.drag.win == w {
		m.drag = drag{}
	}
	if m.focused == w {
		m.focused = nil
		m.focus(m.topShown())
	}
	m.dirty = true
}

// panelKey handles a key for a focused panel window: the inventory's own
// keys, else scrolling keys scroll it; nothing is sent anywhere.
func (m *WM) panelKey(w *Window, k uv.Key) {
	if m.isInventory(w) && m.inventoryKey(k) {
		return
	}
	if m.isHelp(w) && m.helpKey(k) { // choose and change shortcuts (keymap.go)
		return
	}
	page := max(w.contentH()-1, 1)
	switch k.Code {
	case uv.KeyUp:
		m.scrollWindow(w, 1)
	case uv.KeyDown:
		m.scrollWindow(w, -1)
	case uv.KeyPgUp:
		m.scrollWindow(w, page)
	case uv.KeyPgDown:
		m.scrollWindow(w, -page)
	case uv.KeyHome:
		m.scrollWindow(w, w.emu.ScrollbackLen())
	case uv.KeyEnd:
		m.scrollWindow(w, -w.scroll)
	}
}

// writePanel replaces a panel's text with lines (which may hold SGR
// sequences). top: the view starts at the first line; else it stays on the
// lines it showed (a panel refreshed while the user reads it).
func (m *WM) writePanel(w *Window, lines []string, top bool) {
	if s := m.sel; s != nil && s.win == w {
		m.clearSelectionIn(w) // the text under it is replaced
	}
	first := w.emu.ScrollbackLen() - w.scroll // the first line in view
	var b strings.Builder
	b.WriteString("\x1b[0m\x1b[2J\x1b[3J\x1b[H") // a clean slate: screen and history
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		b.WriteString(line)
	}
	w.scroll = 0
	_ = w.feed([]byte("\x1b[0m"))
	w.emu.ClearScrollback()
	_ = w.feed([]byte(b.String()))
	sb := w.emu.ScrollbackLen()
	if top {
		first = 0
	}
	w.scroll = clamp(sb-first, 0, sb)
	m.dirty = true
}

// panelLine is the index (from the first line written) of the absolute
// line a click landed on (selection.go posAt).
func panelLine(w *Window, abs int) int {
	return abs - (w.emu.ScrollbackPushed() - w.emu.ScrollbackLen())
}

// showPanelLines scrolls a panel so that lines [from, to] are in view (from
// first, if they don't all fit).
func showPanelLines(w *Window, from, to int) {
	sb, h := w.emu.ScrollbackLen(), w.contentH()
	first := sb - w.scroll
	if to >= first+h {
		first = to - h + 1
	}
	if from < first {
		first = from
	}
	w.scroll = clamp(sb-first, 0, sb)
}

// panelSelectionBusy reports whether the user is selecting text in w, or
// has text selected there: a refresh then waits, so the text isn't pulled
// from under the mouse and the selection stays to be copied (a click
// elsewhere ends it). A new width is laid out all the same.
func (m *WM) panelSelectionBusy(w *Window, shownW int) bool {
	s := m.sel
	busy := s != nil && s.win == w && (s.dragging || s.visible()) && w.contentW() == shownW
	if busy && !s.dragging { // say so, while it lasts (refreshed every tick)
		m.setBarMessage(m.tabName(w)+": not refreshed while text is selected (click elsewhere to go on)", time.Second)
	}
	return busy
}

// printable drops control and other invisible characters from text a
// program supplied (a title, a process name, a directory, a message) before
// goat draws it or writes it into a panel: tabs and line breaks become
// spaces, the rest is dropped, so no program can put escape sequences or
// cursor moves into goat's own text.
func printable(s string) string {
	keep := func(r rune) bool { // (the joiners hold emoji like 🧑‍💻 together)
		return unicode.IsPrint(r) || r == '\u200d' || r == '\u200c'
	}
	if !strings.ContainsFunc(s, func(r rune) bool { return !keep(r) }) {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case !keep(r):
			return -1
		}
		return r
	}, s)
}
