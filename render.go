package main

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/rivo/uniseg"
)

// scene draws the whole desktop; it implements uv.Drawable.
type scene struct{ m *WM }

func (s scene) Draw(scr uv.Screen, _ uv.Rectangle) {
	m := s.m
	for _, w := range m.windows { // bottom to top: later windows cover earlier
		if !w.background {
			m.drawFrame(scr, w)
		}
		m.drawContent(scr, w)
		m.drawScrollMarker(scr, w)
	}
	m.drawStatus(scr) // last, so windows never cover it
}

// put sets a cell if it lies on screen.
func put(scr uv.Screen, x, y int, c *uv.Cell) {
	b := scr.Bounds()
	if x >= b.Min.X && x < b.Max.X && y >= b.Min.Y && y < b.Max.Y {
		scr.SetCell(x, y, c)
	}
}

// runeCell makes a one-column cell. Screen.SetCell copies the cell, so one
// can be put at many positions.
func runeCell(r rune, st uv.Style) *uv.Cell {
	return &uv.Cell{Content: runeString(r), Width: 1, Style: st}
}

// runeStrings caches the strings of the few characters frames are drawn
// with, so drawing doesn't allocate one per cell.
var runeStrings = map[rune]string{}

func runeString(r rune) string {
	s, ok := runeStrings[r]
	if !ok {
		s = string(r)
		runeStrings[r] = s
	}
	return s
}

// textCells splits s into grapheme cells, at most maxW columns wide.
func textCells(s string, st uv.Style, maxW int) []uv.Cell {
	var out []uv.Cell
	used := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		wd := g.Width()
		if wd <= 0 {
			continue
		}
		if used+wd > maxW {
			break
		}
		out = append(out, uv.Cell{Content: g.Str(), Width: wd, Style: st})
		used += wd
	}
	return out
}

func cellsWidth(cs []uv.Cell) int {
	n := 0
	for _, c := range cs {
		n += c.Width
	}
	return n
}

// putCells draws cells left to right from x; returns the next free column.
func putCells(scr uv.Screen, x, y int, cs []uv.Cell) int {
	for i := range cs {
		put(scr, x, y, &cs[i])
		x += cs[i].Width
	}
	return x
}

func (m *WM) borderColor(w *Window) color.Color {
	switch {
	case w.pinned:
		return m.cfg.Theme.PinnedBorder.C
	case w == m.focused:
		return m.cfg.Theme.FocusedBorder.C
	default:
		return m.cfg.Theme.UnfocusedBorder.C
	}
}

func (m *WM) titleLabel(w *Window) string {
	s := fmt.Sprintf("%d:%s", m.number(w), w.displayTitle(m.cfg.TitleSource))
	if w.scroll > 0 {
		s += fmt.Sprintf(" [-%d]", w.scroll)
	}
	return s
}

// drawFrame draws the frame and title, per the frame style:
//
//	full           compact        none
//	╭─────────╮    ╭─ 1:vim ─╮    ── 1:vim ──
//	│  1:vim  │    │ content │    content
//	├─────────┤    └─────────┘
//	│ content │
//	└─────────┘
//
// The frame takes the window's Alt+b background; otherwise, for compact and
// none, theme.compact_title_bg.
func (m *WM) drawFrame(scr uv.Screen, w *Window) {
	st := uv.Style{Fg: m.borderColor(w)}
	if w == m.focused {
		st.Attrs |= uv.AttrBold
	}
	switch {
	case w.bgColor != nil:
		st.Bg = w.bgColor
	case w.frame != frameFull && m.cfg.Theme.CompactTitleBg != nil:
		st.Bg = m.cfg.Theme.CompactTitleBg.C
	}
	x0, y0, x1, y1 := w.x, w.y, w.x+w.w-1, w.y+w.h-1
	line := func(y int, left, mid, right rune) {
		put(scr, x0, y, runeCell(left, st))
		m := runeCell(mid, st)
		for x := x0 + 1; x < x1; x++ {
			put(scr, x, y, m)
		}
		put(scr, x1, y, runeCell(right, st))
	}
	// titleInLine writes " label " into the top line, starting at column x.
	titleInLine := func(x, maxW int) {
		if !w.decorated {
			return
		}
		if cs := textCells(m.titleLabel(w), st, maxW); len(cs) > 0 {
			put(scr, x, y0, runeCell(' ', st))
			end := putCells(scr, x+1, y0, cs)
			put(scr, end, y0, runeCell(' ', st))
		}
	}

	if w.frame == frameNone {
		line(y0, '─', '─', '─')
		titleInLine(x0+2, w.w-5) // ──␠title␠──
		return
	}

	line(y0, '╭', '─', '╮')
	side := runeCell('│', st)
	for y := y0 + 1; y < y1; y++ {
		put(scr, x0, y, side)
		put(scr, x1, y, side)
	}
	line(y1, '└', '─', '┘')
	m.drawWrapIndicator(scr, w, st)

	if w.frame == frameCompact {
		titleInLine(x0+2, w.w-6) // ╭─␠title␠───╮
		return
	}
	if !w.decorated {
		return
	}
	// Full: title row, centred, then the separator.
	space := runeCell(' ', st)
	for x := x0 + 1; x < x1; x++ {
		put(scr, x, y0+1, space)
	}
	cs := textCells(m.titleLabel(w), st, w.w-4)
	putCells(scr, x0+1+(w.w-2-cellsWidth(cs))/2, y0+1, cs)
	line(y0+2, '├', '─', '┤')
}

// drawContent copies the window's terminal cells (colors, bold, underline,
// reverse, wide characters...) into its content area, clipped to the screen.
func (m *WM) drawContent(scr uv.Screen, w *Window) {
	cx, cy, cw, ch := w.contentX(), w.contentY(), w.contentW(), w.contentH()
	b := scr.Bounds()

	// Selection in this window, if any (computed once, not per cell).
	var selA, selB selPos
	hasSel := m.sel.visible() && m.sel.win == w
	if hasSel {
		selA, selB = m.sel.bounds()
	}
	// Cells without their own colors get the window's (Alt+b).
	blank := uv.EmptyCell
	blank.Style.Bg = w.bgColor

	// One scratch cell for the whole window: SetCell copies it (a cell
	// taken by address per position escapes, one allocation per cell).
	cc := new(uv.Cell)
	for row := range ch {
		sy := cy + row
		if sy < b.Min.Y || sy >= b.Max.Y {
			continue
		}
		rowSel, line := false, 0
		if hasSel {
			line = w.absLine(row)
			rowSel = line >= selA.line && line <= selB.line
		}
		for col := 0; col < cw; {
			c := w.cellAt(col+w.hscroll, row)
			width := max(c.Width, 1)
			sx := cx + col
			if col+width > cw || sx < b.Min.X || sx+width > b.Max.X {
				// A wide character cut by an edge: show blanks instead.
				for i := range width {
					if col+i < cw {
						put(scr, sx+i, sy, &blank)
					}
				}
			} else {
				*cc = *c
				if c.IsZero() {
					*cc = uv.EmptyCell
				}
				if cc.Style.Bg == nil {
					cc.Style.Bg = w.bgColor
				}
				if cc.Style.Fg == nil {
					cc.Style.Fg = w.fgColor
				}
				if rowSel {
					if p := (selPos{line, col + w.hscroll}); !p.before(selA) && !selB.before(p) {
						cc.Style.Attrs ^= uv.AttrReverse
					}
				}
				put(scr, sx, sy, cc)
			}
			col += width
		}
	}
}

// wrapLabel is the bottom-border label for w's line wrapping.
func wrapLabel(w *Window) string {
	switch {
	case !w.nowrap:
		return "wrap"
	case w.hscroll > 0:
		return fmt.Sprintf("nowrap +%d", w.hscroll)
	}
	return "nowrap"
}

// drawWrapIndicator writes the wrapping state into the bottom border, near
// the right corner: ──── nowrap +40 ─┘ (when the window is wide enough).
func (m *WM) drawWrapIndicator(scr uv.Screen, w *Window, st uv.Style) {
	label := " " + wrapLabel(w) + " "
	x := w.x + w.w - 2 - len(label)
	if x < w.x+2 {
		return
	}
	putCells(scr, x, w.y+w.h-1, textCells(label, st, len(label)))
}

// scrollThumb is where the scroll marker goes, in content rows: the rows
// from pos to pos+size-1 of an h-row window, for a view scrolled back by
// scroll lines out of sb lines of history (scroll > 0, sb >= scroll).
func scrollThumb(h, sb, scroll int) (pos, size int) {
	total := sb + h
	size = clamp(h*h/total, 1, h)
	pos = (sb - scroll) * (h - size) / sb // oldest line: top; newest: bottom
	return pos, size
}

// drawScrollMarker shows, while a window is scrolled back, where the view
// is in its history: a thick bar on the right border, or, for windows
// without one (frame "none", the background shell), in the last column.
func (m *WM) drawScrollMarker(scr uv.Screen, w *Window) {
	sb := w.emu.ScrollbackLen()
	h := w.contentH()
	if w.scroll <= 0 || sb <= 0 || h <= 0 || w.contentW() <= 0 {
		return
	}
	pos, size := scrollThumb(h, sb, w.scroll)
	st := uv.Style{Fg: m.cfg.Theme.FocusedBorder.C, Attrs: uv.AttrBold}
	x, ch := w.x+w.w-1, '┃'
	overlay := w.background || w.frame == frameNone // no right border
	if overlay {
		x, ch = w.contentX()+w.contentW()-1, '▐'
	}
	switch { // the background of the border or content it sits on
	case w.bgColor != nil:
		st.Bg = w.bgColor
	case !overlay && w.frame != frameFull && m.cfg.Theme.CompactTitleBg != nil:
		st.Bg = m.cfg.Theme.CompactTitleBg.C
	}
	for i := range size {
		put(scr, x, w.contentY()+pos+i, runeCell(ch, st))
	}
}

// drawStatus draws the bottom line: a numbered tab per window (the focused
// one highlighted; click to focus), any message, and the clock. When space is
// short the clock wins, then the tabs; the message is shortened.
func (m *WM) drawStatus(scr uv.Screen) {
	y := m.rows - 1
	base := uv.Style{Fg: m.cfg.Theme.StatusFg.C, Bg: m.cfg.Theme.StatusBg.C}
	fill := &uv.Cell{Content: " ", Width: 1, Style: base}
	for x := range m.cols {
		put(scr, x, y, fill)
	}

	clockSt := uv.Style{Fg: m.cfg.Theme.ClockFg.C, Bg: m.cfg.Theme.ClockBg.C, Attrs: uv.AttrBold}
	clock := textCells(" "+time.Now().Format("15:04")+" ", clockSt, m.cols)
	clockX := m.cols - cellsWidth(clock)
	putCells(scr, clockX, y, clock)

	m.tabs = m.tabs[:0]
	x := 0
	for _, w := range m.cycle() {
		label := fmt.Sprintf(" %d:%s", m.number(w), w.displayTitle(m.cfg.TitleSource))
		if w.pinned {
			label += "*"
		}
		label += " "
		st := base
		if w == m.focused {
			st.Fg = m.cfg.Theme.FocusedBorder.C
			st.Attrs |= uv.AttrBold | uv.AttrReverse
		}
		cs := textCells(label, st, max(clockX-x, 0))
		if cellsWidth(cs) < uniseg.StringWidth(label) {
			break // only whole tabs
		}
		end := putCells(scr, x, y, cs)
		m.tabs = append(m.tabs, tab{x0: x, x1: end, win: w})
		x = end
	}

	// Right of the tabs, from the clock leftwards: the directory, then the
	// message. The message (short-lived) gets space first; the directory
	// takes what is left, keeping its end ("…/src/goat").
	end := clockX
	room := clockX - x - 1 // one blank after the tabs
	var msgCells []uv.Cell
	if m.status != "" && room > 1 {
		st := base
		st.Fg = m.cfg.Theme.HintText.C
		msgCells = textCells(fitStart(m.status, room), st, room)
		room -= cellsWidth(msgCells) + 1
	}
	if m.cfg.StatusShowDir && m.focused != nil && m.focused.dir != "" && room > 2 {
		dir := fitEnd(homeShort(m.focused.dir, m.home), room-1)
		cs := textCells(dir, base, room-1)
		end -= cellsWidth(cs)
		putCells(scr, end, y, cs)
		end-- // a blank between message and directory
	}
	if len(msgCells) > 0 {
		putCells(scr, end-cellsWidth(msgCells)-1, y, msgCells)
	}
}

// fitStart shortens s to at most w columns by cutting its end ("abc…").
func fitStart(s string, w int) string {
	if uniseg.StringWidth(s) <= w {
		return s
	}
	if w < 2 {
		return ""
	}
	out := ""
	for _, c := range textCells(s, uv.Style{}, w-1) {
		out += c.Content
	}
	return out + "…"
}

// fitEnd shortens s to at most w columns by cutting its start ("…xyz").
func fitEnd(s string, w int) string {
	if uniseg.StringWidth(s) <= w {
		return s
	}
	if w < 2 {
		return ""
	}
	cs := textCells(s, uv.Style{}, 1<<30)
	used, i := 0, len(cs)
	for i > 0 && used+cs[i-1].Width <= w-1 {
		i--
		used += cs[i].Width
	}
	out := "…"
	for _, c := range cs[i:] {
		out += c.Content
	}
	return out
}

// homeShort writes a path under home as ~/...
func homeShort(dir, home string) string {
	switch {
	case home == "" || home == "/":
		return dir
	case dir == home:
		return "~"
	case strings.HasPrefix(dir, home+"/"):
		return "~" + dir[len(home):]
	}
	return dir
}

// cursorFor returns where the host cursor should be: the focused program's
// cursor, unless it is hidden, scrolled away, off screen or under another
// window.
func (m *WM) cursorFor() (x, y int, ok bool) {
	w := m.focused
	if w == nil || w.closed || !w.cursorVisible || w.scroll > 0 {
		return 0, 0, false
	}
	p := w.emu.CursorPosition()
	p.X -= w.hscroll // no-wrap: the view may be scrolled sideways
	if p.X < 0 || p.Y < 0 || p.X >= w.contentW() || p.Y >= w.contentH() {
		return 0, 0, false
	}
	x, y = w.contentX()+p.X, w.contentY()+p.Y
	// Not under another window, and not on the status bar (the last row),
	// which is drawn over every window.
	if x < 0 || y < 0 || x >= m.cols || y >= m.rows-1 || m.topAt(x, y) != w {
		return 0, 0, false
	}
	return x, y, true
}
