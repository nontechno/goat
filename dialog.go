package main

import (
	"fmt"
	"image/color"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The quit dialog: Alt+q (keys.quit) with floating windows open asks
// first, so a slip of the finger doesn't end everything running in them.
//
//	╭─ Quit goat? ───────────────────────────────╮
//	│                                            │
//	│ 2 windows are open; quitting ends the      │
//	│ programs in them.                          │
//	│ 1:vim  2:tail logs                         │
//	│                                            │
//	│            Quit     > Cancel <             │
//	│        y: quit   n / Esc: cancel           │
//	╰────────────────────────────────────────────╯
//
// While it is up, the windows' programs keep running and their output keeps
// showing behind it; only your keys, mouse and pastes go to the dialog. The selected button is a solid bright one marked > <, the other a
// flat gray one. Cancel is the default: Enter on it, n or Esc closes the dialog;
// y, Enter on Quit, or Alt+q again quits. Left / Right / Tab move between
// the buttons; a click on one presses it.

type quitDialog struct {
	quit bool // the Quit button is selected (else Cancel)
	// Where the buttons were drawn, for clicks.
	quitBtn, cancelBtn uv.Rectangle
}

// floatingWindows are the windows quitting would close besides the
// background shell (not the events window: that's goat's own).
func (m *WM) floatingWindows() []*Window {
	var out []*Window
	for _, w := range m.windows {
		if !w.background && !m.isPanel(w) {
			out = append(out, w)
		}
	}
	return out
}

// askQuit is Alt+q: quit at once with only the background shell, else ask.
func (m *WM) askQuit() {
	if len(m.floatingWindows()) == 0 || !m.local.confirmQuit { // local.toml confirm_quit = false: no dialog
		m.quit = true
		return
	}
	m.cancelEsc()
	m.quitAsk = &quitDialog{}
	m.dirty = true
}

func (m *WM) closeQuitDialog() {
	m.quitAsk = nil
	m.dirty = true
}

// quitDialogKey handles a key while the dialog is up: every key goes here.
func (m *WM) quitDialogKey(k uv.Key) {
	d := m.quitAsk
	r, isChar := bindingRune(k)
	if !isChar && m.macOption { // macOS Option+q typed as œ
		if k2, ok := macOptionKey(k); ok {
			k, r, isChar = k2, rune(k2.Code), true
		}
	}
	quitKey := []rune(m.cfg.Keys.Quit)
	switch {
	case isChar && k.Mod.Contains(uv.ModAlt) && len(quitKey) == 1 && r == quitKey[0]: // Alt+q again
		m.quit = true
	case k.Code == uv.KeyEscape:
		m.closeQuitDialog()
	case k.Code == uv.KeyEnter:
		if d.quit {
			m.quit = true
		} else {
			m.closeQuitDialog()
		}
	case k.Code == uv.KeyLeft, k.Code == uv.KeyRight, k.Code == uv.KeyTab:
		d.quit = !d.quit
		m.dirty = true
	case isChar && k.Mod&^uv.ModShift == 0 && (r == 'y' || r == 'Y'):
		m.quit = true
	case isChar && k.Mod&^uv.ModShift == 0 && (r == 'n' || r == 'N'):
		m.closeQuitDialog()
	}
}

// quitDialogMouse handles the mouse while the dialog is up: a click on a
// button presses it; nothing else reaches the windows.
func (m *WM) quitDialogMouse(ev uv.MouseEvent) {
	click, ok := ev.(uv.MouseClickEvent)
	if !ok || click.Button != uv.MouseLeft {
		return
	}
	p := uv.Pos(click.X, click.Y)
	switch d := m.quitAsk; {
	case p.In(d.quitBtn):
		m.quit = true
	case p.In(d.cancelBtn):
		m.closeQuitDialog()
	}
}

// drawQuitDialog draws the dialog in the middle of the screen, over
// everything.
func (m *WM) drawQuitDialog(scr uv.Screen) {
	d := m.quitAsk
	wins := m.floatingWindows()
	width := min(46, m.cols)
	inner := max(width-4, 1) // text width: border and a space each side

	var lines []string
	n := len(wins)
	msg := fmt.Sprintf("%d windows are open; quitting ends the programs in them.", n)
	if n == 1 {
		msg = "1 window is open; quitting ends the program in it."
	}
	lines = append(lines, "")
	lines = append(lines, wrapWords(msg, inner)...)
	var names []string
	for _, w := range wins {
		names = append(names, m.tabName(w))
	}
	lines = append(lines, fitEnd(strings.Join(names, "  "), inner))
	lines = append(lines, "")
	height := len(lines) + 4 // borders, buttons, key hint
	x0 := max((m.cols-width)/2, 0)
	y0 := max((m.rows-height)/2, 0)

	base, hint := m.drawDialogBox(scr, x0, y0, width, height, "Quit goat?")
	for i, l := range lines {
		putCells(scr, x0+2, y0+1+i, textCells(l, base, inner))
	}

	// The buttons. The selected one can't be mistaken: a solid bright
	// fill (red for Quit, the theme's highlight for Cancel), bold text and
	// > < markers; the other is a flat dim gray, without markers.
	q, c := m.buttonCells("Quit", d.quit, true, base.Bg), m.buttonCells("Cancel", !d.quit, false, base.Bg)
	gap := 4
	bx := x0 + (width-len(q)-gap-len(c))/2
	by := y0 + 1 + len(lines)
	putCells(scr, bx, by, q)
	putCells(scr, bx+len(q)+gap, by, c)
	d.quitBtn = uv.Rect(bx, by, len(q), 1)
	d.cancelBtn = uv.Rect(bx+len(q)+gap, by, len(c), 1)

	keys := "y: quit   n / Esc: cancel"
	putCells(scr, x0+max((width-len(keys))/2, 1), by+1, textCells(keys, hint, inner))
}

// dialogStyles are the colors of goat's dialogs: the status bar's, with a
// border and hints in the theme's colors made to show on it.
func (m *WM) dialogStyles() (base, border, hint uv.Style) {
	base = uv.Style{Fg: m.cfg.Theme.StatusFg.C, Bg: m.cfg.Theme.StatusBg.C}
	border = base
	border.Fg = m.visibleOn(m.cfg.Theme.FocusedBorder.C, base.Bg, contrastBorder)
	border.Attrs |= uv.AttrBold
	hint = base
	hint.Fg = m.visibleOn(m.cfg.Theme.HintText.C, base.Bg, contrastLabel)
	return base, border, hint
}

// drawDialogBox draws a dialog's box, filled, with the title in its top
// line, and returns the styles for its text and hints.
func (m *WM) drawDialogBox(scr uv.Screen, x0, y0, width, height int, title string) (base, hint uv.Style) {
	base, border, hint := m.dialogStyles()
	for y := y0; y < y0+height; y++ {
		for x := x0; x < x0+width; x++ {
			ch := ' '
			st := base
			switch {
			case y == y0 && x == x0:
				ch, st = frameTopLeft, border
			case y == y0 && x == x0+width-1:
				ch, st = frameTopRight, border
			case y == y0+height-1 && x == x0:
				ch, st = dialogBottomLeft, border
			case y == y0+height-1 && x == x0+width-1:
				ch, st = dialogBottomRight, border
			case y == y0 || y == y0+height-1:
				ch, st = frameH, border
			case x == x0 || x == x0+width-1:
				ch, st = frameV, border
			}
			put(scr, x, y, runeCell(ch, st))
		}
	}
	putCells(scr, x0+2, y0, textCells(" "+title+" ", border, width-4))
	return base, hint
}

// Button colors (palette numbers, the same on every terminal).
var (
	btnDangerBg = ansi.IndexedColor(196) // selected Quit: bright red
	btnIdleBg   = ansi.IndexedColor(239) // not selected: flat gray
	btnIdleFg   = ansi.IndexedColor(249)
)

// buttonCells draws a dialog button: "> Label <" when selected, "  Label  "
// otherwise (the same width, so nothing moves when the selection does).
func (m *WM) buttonCells(label string, selected, danger bool, boxBg color.Color) []uv.Cell {
	text := buttonMarkLeft + label + buttonMarkRight
	if !selected { // the marks' width in spaces, so nothing moves
		st := uv.Style{Fg: btnIdleFg, Bg: btnIdleBg}
		text = strings.Repeat(" ", textWidth(buttonMarkLeft)) + label + strings.Repeat(" ", textWidth(buttonMarkRight))
		return textCells(text, st, textWidth(text))
	}
	var bg color.Color = btnDangerBg
	if !danger {
		bg = m.visibleOn(m.cfg.Theme.FocusedBorder.C, boxBg, contrastLabel)
	}
	st := uv.Style{Fg: blackOrWhite(bg), Bg: bg, Attrs: uv.AttrBold}
	return textCells(text, st, textWidth(text))
}

// blackOrWhite is whichever of black and white reads better on bg.
func blackOrWhite(bg color.Color) color.Color {
	black, white := ansi.IndexedColor(16), ansi.IndexedColor(231)
	lb := luminance(bg)
	if contrastRatio(luminance(white), lb) >= contrastRatio(luminance(black), lb) {
		return white
	}
	return black
}

// wrapWords breaks s into lines of at most width columns, at spaces.
func wrapWords(s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		switch {
		case line == "":
			line = w
		case len(line)+1+len(w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
