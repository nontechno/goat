package main

import (
	"fmt"
	"image/color"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// The palette dialog (Alt+t, keys.palette): choose the active window's
// colors, a background + text pair from terminal-color-combinations.json
// (combos.go):
//
//	╭───────────────┬───────────────┬────────palette─╮
//	│ Minty Fresh … │ Pea Soup …    │ Spice Rack …   │
//	│ …             ╔═══════════════╗ …              │
//	│ …             ║ Blue on Blue  ║ …              │
//	│ …             ╚═══════════════╝ …              │
//	╰ theme: dark · lingo: Pedantic ─ … Enter keep ─╯
//
// The palettes are the theme's 36 pairs (dark, in-between or light
// backgrounds), each named in the lingo (Pedantic, Funny, …) and drawn in
// its own colors; the chosen one has a double box in its colors (over the
// items above and below it). The theme and the lingo are in the bottom
// line: t / l (or a click on them) go to the next one, Shift+t / Shift+l
// back.
//
// The window shows each pair as it is chosen (arrows, Home, End, a click).
// Enter (or a double click) keeps it, Esc (or Alt+t again) puts the
// window's colors back. Delete chooses no palette: the terminal's own
// colors. The quit shortcut (Alt+q) closes the dialog, as Esc, and quits.
//
// Kept for the background window, it is saved in local.toml (like its
// colors always were); for goat's own windows (help, status, inventory,
// events) in system.toml (system.go). The lingo is saved in local.toml
// (palette_lingo).
//
// Like the other dialogs, it is modal: the windows' programs keep running
// and their output keeps showing; keys, clicks and pastes go to the dialog.

// paletteDialog is the dialog's state while it is up.
type paletteDialog struct {
	target *Window
	theme  int // index into comboThemes
	lingo  int // index into comboLingos
	pos    int // position in the theme's list (0-35); -1: no palette
	// The window's colors when the dialog opened (Esc puts them back).
	bg, fg color.Color
	pal    int
	// Where things were drawn, for clicks.
	themeAt, lingoAt uv.Rectangle   // the theme and lingo in the bottom line
	cells            []uv.Rectangle // the palette items, by position
	box              uv.Rectangle
	// The last click on a palette item, for double clicks.
	clickAt  time.Time
	clickPos int
}

// Dialog geometry: the palette's rows and columns, and how wide a palette
// item may get.
const (
	palRows    = 12
	palCols    = 3
	palItemMax = 34 // names are at most 32 characters, plus a space each side
	palItemMin = 6
)

// openPalette is Alt+t: open the dialog for the active window.
func (m *WM) openPalette() {
	w := m.focused
	if w == nil {
		w = m.bg
	}
	if w == nil || w.closed {
		return
	}
	if m.combos == nil {
		m.setBarMessage(noCombos, 6*time.Second)
		return
	}
	m.cancelEsc()
	d := &paletteDialog{target: w, lingo: m.lingo, bg: w.bgColor, fg: w.fgColor, pal: w.pal, pos: -1, clickPos: -1}
	if i := w.pal - 1; m.combos.get(i) != nil {
		d.theme, d.pos = m.combos.all[i].theme, m.combos.position(i)
	} else if m.backgroundTone(w) == toneLight {
		d.theme = 2
	}
	m.palDlg = d
	m.dirty = true
}

// palCount is how many palettes the dialog's theme has.
func (m *WM) palCount() int { return len(m.combos.byTheme[m.palDlg.theme]) }

// palChosen is the chosen pair's index in the file (-1: none).
func (m *WM) palChosen() int {
	d := m.palDlg
	list := m.combos.byTheme[d.theme]
	if d.pos < 0 || d.pos >= len(list) {
		return -1
	}
	return list[d.pos]
}

// palPreview shows the chosen pair on the window.
func (m *WM) palPreview() {
	d := m.palDlg
	if d.target.closed {
		return
	}
	m.setCombo(d.target, m.palChosen())
}

// closePalette closes the dialog: keep the choice, or put the window's
// colors back.
func (m *WM) closePalette(keep bool) {
	d := m.palDlg
	m.palDlg = nil
	m.dirty = true
	w := d.target
	if w.closed {
		return
	}
	if !keep {
		w.bgColor, w.fgColor, w.pal = d.bg, d.fg, d.pal
		m.applyHostColors(w)
		return
	}
	if d.lingo != m.lingo {
		m.lingo = d.lingo
		m.saveLingo()
	}
	name := "the terminal's own colors"
	if i := w.pal - 1; i >= 0 {
		name = fmt.Sprintf("%s (%s, %d)", m.comboName(i), comboThemeShown[m.combos.all[i].theme], m.combos.all[i].id)
	}
	switch {
	case m.isPanel(w):
		m.savePanelTheme(w) // system.toml
	case w.background:
		m.saveDesktop() // local.toml
	}
	m.setBarMessage(m.tabName(w)+": "+name, 4*time.Second)
}

// saveLingo writes the lingo to local.toml.
func (m *WM) saveLingo() {
	if m.localPath == "" {
		return
	}
	v := tomlString(comboLingos[m.lingo])
	if err := writeTopLevelKeys(m.localPath, map[string]*string{"palette_lingo": &v}); err != nil {
		logger.Warn("saving palette_lingo", "file", m.localPath, "err", err)
	}
}

// paletteKey handles a key while the dialog is up: every key goes here.
func (m *WM) paletteKey(k uv.Key) {
	d := m.palDlg
	r, isChar := bindingRune(k)
	if !isChar && m.macOption {
		if k2, ok := macOptionKey(k); ok {
			k, r, isChar = k2, rune(k2.Code), true
		}
	}
	n := m.palCount()
	plain := isChar && !k.Mod.Contains(uv.ModAlt) && !k.Mod.Contains(uv.ModCtrl)
	switch {
	case k.Code == uv.KeyEscape:
		m.closePalette(false)
		return
	case isChar && k.Mod.Contains(uv.ModAlt) && string(r) == m.cfg.Keys.Palette: // Alt+t again
		m.closePalette(false)
		return
	case k.Code == uv.KeyEnter:
		m.closePalette(true)
		return
	case k.Code == uv.KeyDelete || k.Code == uv.KeyBackspace:
		d.pos = -1
		m.palPreview()
	case plain && (r == 't' || r == 'T'): // the next theme (Shift: the one before)
		m.palTheme(cycle(d.theme, len(comboThemes), r == 'T'))
	case plain && (r == 'l' || r == 'L'): // the next lingo
		d.lingo = cycle(d.lingo, len(comboLingos), r == 'L')
	default: // the palette: down the columns, one column after another
		p := d.pos
		switch k.Code {
		case uv.KeyUp:
			p--
		case uv.KeyDown:
			p++
		case uv.KeyLeft:
			if p >= palRows {
				p -= palRows
			}
		case uv.KeyRight:
			if p < 0 {
				p = 0
			} else if p+palRows < n {
				p += palRows
			}
		case uv.KeyHome:
			p = 0
		case uv.KeyEnd:
			p = n - 1
		default:
			return
		}
		if p != d.pos && n > 0 {
			d.pos = clamp(p, 0, n-1)
			m.palPreview()
		}
	}
	m.dirty = true
}

// cycle is the next of n choices after i (back: the one before), round.
func cycle(i, n int, back bool) int {
	if back {
		return (i + n - 1) % n
	}
	return (i + 1) % n
}

// palTheme switches the dialog to theme t, keeping the palette's place.
func (m *WM) palTheme(t int) {
	d := m.palDlg
	t = clamp(t, 0, len(comboThemes)-1)
	if t == d.theme {
		return
	}
	d.theme = t
	if d.pos >= 0 {
		d.pos = min(d.pos, max(m.palCount()-1, 0))
		m.palPreview()
	}
}

// paletteMouse handles the mouse while the dialog is up.
func (m *WM) paletteMouse(ev uv.MouseEvent) {
	click, ok := ev.(uv.MouseClickEvent)
	if !ok || click.Button != uv.MouseLeft {
		return
	}
	d := m.palDlg
	p := uv.Pos(click.X, click.Y)
	switch {
	case p.In(d.themeAt):
		m.palTheme(cycle(d.theme, len(comboThemes), false))
	case p.In(d.lingoAt):
		d.lingo = cycle(d.lingo, len(comboLingos), false)
	}
	for i, r := range d.cells {
		if !p.In(r) {
			continue
		}
		now := time.Now()
		double := d.clickPos == i && now.Sub(d.clickAt) < multiClickTime
		d.clickAt, d.clickPos = now, i
		if d.pos != i {
			d.pos = i
			m.palPreview()
		}
		if double {
			m.closePalette(true)
			return
		}
		break
	}
	m.dirty = true
}

// drawPalette draws the dialog, centered.
func (m *WM) drawPalette(scr uv.Screen) {
	d := m.palDlg
	base, border, hint := m.dialogStyles()
	itemW := clamp((m.cols-2-(palCols-1))/palCols, palItemMin, palItemMax)
	width := 1 + palCols*itemW + (palCols - 1) + 1
	height := palRows + 2
	x0, y0 := max((m.cols-width)/2, 0), max((m.areaH()-height)/2, 0)
	x1, y1 := x0+width-1, y0+height-1
	d.box = uv.Rect(x0, y0, width, height)
	colX := func(c int) int { return x0 + c*(itemW+1) } // the line left of column c

	// The box and its column lines, all in the frame's color.
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			put(scr, x, y, runeCell(' ', base))
		}
	}
	for x := x0; x <= x1; x++ {
		put(scr, x, y0, runeCell(frameH, border))
		put(scr, x, y1, runeCell(frameH, border))
	}
	for y := y0; y <= y1; y++ {
		for c := 0; c <= palCols; c++ {
			put(scr, colX(c), y, runeCell(frameV, border))
		}
	}
	put(scr, x0, y0, runeCell(frameTopLeft, border))
	put(scr, x1, y0, runeCell(frameTopRight, border))
	put(scr, x0, y1, runeCell(dialogBottomLeft, border))
	put(scr, x1, y1, runeCell(dialogBottomRight, border))
	for c := 1; c < palCols; c++ {
		put(scr, colX(c), y0, runeCell(dialogTeeDown, border))
		put(scr, colX(c), y1, runeCell(dialogTeeUp, border))
	}
	if cs := textCells("palette", border, width-4); len(cs) > 0 {
		putCells(scr, x1-cellsWidth(cs)-1, y0, cs)
	}

	// The palette: column by column, each item in its own colors.
	list := m.combos.byTheme[d.theme]
	d.cells = d.cells[:0]
	for p, i := range list {
		c := &m.combos.all[i]
		col, row := p/palRows, p%palRows
		if col >= palCols {
			break
		}
		x, y := colX(col)+1, y0+1+row
		st := uv.Style{Fg: m.combos.fg(c), Bg: m.combos.bg(c)}
		for xx := x; xx < x+itemW; xx++ {
			put(scr, xx, y, runeCell(' ', st))
		}
		putCells(scr, x+1, y, textCells(fitStart(c.names[d.lingo], itemW-2), st, itemW-2))
		d.cells = append(d.cells, uv.Rect(x, y, itemW, 1))
	}
	// The bottom line: the theme and the lingo on the left (click them, or
	// t / l), what to press on the right.
	x := x0 + 1
	field := func(label, value string) uv.Rectangle {
		cs := textCells(" "+label+": ", hint, x1-x)
		start := x
		x = putCells(scr, x, y1, cs)
		cs = textCells(value+" ", border, x1-x)
		x = putCells(scr, x, y1, cs)
		return uv.Rect(start, y1, x-start, 1)
	}
	d.themeAt = field("theme", comboThemeShown[d.theme])
	if x < x1-1 {
		x = putCells(scr, x, y1, textCells("·", hint, 1))
	}
	d.lingoAt = field("lingo", comboLingos[d.lingo])
	help := " t theme · l lingo · arrows choose · Enter keep · Esc cancel "
	if room := x1 - x - 2; room > 0 {
		if cs := textCells(fitStart(help, room), hint, room); len(cs) > 0 {
			putCells(scr, x1-cellsWidth(cs)-1, y1, cs)
		}
	}
	// The chosen one's double box, in its own colors, last: over the items
	// above and below it, and over the dialog's frame (and the bottom
	// line's text) where it reaches it.
	if d.pos >= 0 && d.pos < len(list) {
		c := &m.combos.all[list[d.pos]]
		st := uv.Style{Fg: m.combos.fg(c), Bg: m.combos.bg(c), Attrs: uv.AttrBold}
		col, row := d.pos/palRows, d.pos%palRows
		l, r := colX(col), colX(col)+itemW+1
		y := y0 + 1 + row
		put(scr, l, y-1, runeCell(paletteTL, st))
		put(scr, r, y-1, runeCell(paletteTR, st))
		put(scr, l, y+1, runeCell(paletteBL, st))
		put(scr, r, y+1, runeCell(paletteBR, st))
		for x := l + 1; x < r; x++ {
			put(scr, x, y-1, runeCell(paletteH, st))
			put(scr, x, y+1, runeCell(paletteH, st))
		}
		put(scr, l, y, runeCell(paletteV, st))
		put(scr, r, y, runeCell(paletteV, st))
	}
}
