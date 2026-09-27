package main

import (
	"strings"
	"time"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
)

// Text selection with the mouse.
//
// Drag with the left button inside a window to select; the text is copied to
// the clipboard when the button is released. Double-click selects a word,
// triple-click a line. In programs that use the mouse themselves (vim, htop,
// ...), hold Shift while dragging.
//
// Positions are in "absolute" lines: scrollback history followed by the
// screen, so a selection stays on the same text while output scrolls it
// into history and while the view is scrolled back.

type selMode int

const (
	selChar selMode = iota
	selWord
	selLine
)

type selPos struct{ line, col int }

func (a selPos) before(b selPos) bool {
	return a.line < b.line || a.line == b.line && a.col < b.col
}

type selection struct {
	win          *Window
	anchor, head selPos
	mode         selMode
	dragging     bool
	moved        bool // a char-mode drag only selects once the pointer moves
}

const multiClickTime = 400 * time.Millisecond

// absLine maps a content row on screen to an absolute line.
func (w *Window) absLine(row int) int {
	return w.emu.ScrollbackLen() - w.scroll + row
}

// cellAtAbs returns the cell at an absolute line (nil if none).
func (w *Window) cellAtAbs(col, line int) *uv.Cell {
	sb := w.emu.ScrollbackLen()
	if line < sb {
		return w.emu.ScrollbackCellAt(col, line)
	}
	return w.emu.CellAt(col, line-sb)
}

// posAt converts a screen point to a selection position in w, clamped to the
// content area.
func (w *Window) posAt(x, y int) selPos {
	col := clamp(x-w.contentX(), 0, w.contentW()-1)
	row := clamp(y-w.contentY(), 0, w.contentH()-1)
	return selPos{line: w.absLine(row), col: col}
}

// lineRunes returns the characters of an absolute line, one per column; both
// columns of a wide character hold that character.
func (w *Window) lineRunes(line int) []rune {
	out := make([]rune, w.contentW())
	for col := range out {
		c := w.cellAtAbs(col, line)
		switch {
		case c == nil:
			out[col] = ' '
		case c.Content == "" && c.Width == 0 && col > 0:
			out[col] = out[col-1] // second half of a wide character
		case c.Content == "":
			out[col] = ' '
		default:
			out[col] = []rune(c.Content)[0]
		}
	}
	return out
}

// isContinuation reports whether (col, line) is the second half of a wide
// character.
func (w *Window) isContinuation(col, line int) bool {
	c := w.cellAtAbs(col, line)
	return col > 0 && c != nil && c.Content == "" && c.Width == 0
}

func isWordRune(r rune) bool {
	return r != 0 && !unicode.IsSpace(r) && !strings.ContainsRune("\"'`()[]{}<>|;,", r)
}

// bounds returns the ordered, mode-expanded start and end (inclusive).
func (s *selection) bounds() (selPos, selPos) {
	a, b := s.anchor, s.head
	if b.before(a) {
		a, b = b, a
	}
	w := s.win
	// A selection starting on the second half of a wide character starts at
	// the character itself.
	for a.col > 0 && w.isContinuation(a.col, a.line) {
		a.col--
	}
	switch s.mode {
	case selLine:
		a.col, b.col = 0, w.contentW()-1
	case selWord:
		ra := w.lineRunes(a.line)
		for a.col > 0 && isWordRune(ra[a.col-1]) && isWordRune(ra[a.col]) {
			a.col--
		}
		rb := w.lineRunes(b.line)
		for b.col < len(rb)-1 && isWordRune(rb[b.col]) && isWordRune(rb[b.col+1]) {
			b.col++
		}
	}
	return a, b
}

// visible reports whether there is anything selected to show or copy.
func (s *selection) visible() bool {
	return s != nil && s.win != nil && (s.mode != selChar || s.moved)
}

// contains reports whether absolute (line, col) is selected.
func (s *selection) contains(w *Window, line, col int) bool {
	if !s.visible() || s.win != w {
		return false
	}
	a, b := s.bounds()
	p := selPos{line, col}
	return !p.before(a) && !b.before(p)
}

// text returns the selected text: trailing blanks trimmed on each line,
// lines joined with newlines.
func (s *selection) text() string {
	if !s.visible() {
		return ""
	}
	a, b := s.bounds()
	w := s.win
	var lines []string
	for line := a.line; line <= b.line; line++ {
		from, to := 0, w.contentW()-1
		if line == a.line {
			from = a.col
		}
		if line == b.line {
			to = b.col
		}
		var sb strings.Builder
		for col := from; col <= to && col < w.contentW(); col++ {
			c := w.cellAtAbs(col, line)
			switch {
			case c == nil:
				sb.WriteByte(' ')
			case c.Content == "" && c.Width == 0:
				// second half of a wide character: already written
			case c.Content == "":
				sb.WriteByte(' ')
			default:
				sb.WriteString(c.Content)
			}
		}
		lines = append(lines, strings.TrimRight(sb.String(), " "))
	}
	return strings.Join(lines, "\n")
}

// ---- WM integration ----------------------------------------------------------

// startSelection begins a selection at a click inside w's content.
func (m *WM) startSelection(w *Window, x, y int) {
	p := w.posAt(x, y)
	now := time.Now()
	if m.lastClickWin == w && now.Sub(m.lastClick) < multiClickTime &&
		abs(p.line-m.lastClickPos.line) == 0 && abs(p.col-m.lastClickPos.col) <= 1 {
		m.clicks = min(m.clicks+1, 3)
	} else {
		m.clicks = 1
	}
	m.lastClick, m.lastClickPos, m.lastClickWin = now, p, w
	m.sel = &selection{win: w, anchor: p, head: p, mode: selMode(m.clicks - 1), dragging: true}
	m.drag = drag{kind: dragSelect, win: w}
	m.dirty = true
}

func (m *WM) extendSelection(x, y int) {
	s := m.sel
	if s == nil || !s.dragging {
		return
	}
	p := s.win.posAt(x, y)
	if p != s.head {
		s.head = p
		s.moved = true
		m.dirty = true
	}
}

// finishSelection ends a drag and copies the text.
func (m *WM) finishSelection() {
	s := m.sel
	if s == nil {
		return
	}
	s.dragging = false
	if !s.visible() { // a plain click: nothing selected
		m.clearSelection()
		return
	}
	if t := s.text(); t != "" && m.copy != nil {
		m.copy(t, "")
	}
}

func (m *WM) clearSelection() {
	if m.sel != nil {
		m.sel = nil
		m.dirty = true
	}
}

// clearSelectionIn drops the selection if it belongs to w.
func (m *WM) clearSelectionIn(w *Window) {
	if m.sel != nil && m.sel.win == w {
		m.clearSelection()
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
