package main

import (
	"fmt"
	"image/color"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// The palette dialog (Alt+t, keys.palette): choose the active window's
// colors, a background + text pair from terminal-color-combinations.json
// (combos.go). Three lists, one item chosen in each:
//
//	╭─────────theme─┬───────────────────────────────────────palette─╮
//	│ dark          │ Minty Fresh … │ Pea Soup …    │ Spice Rack …  │
//	│ in-between    │ …             │ …             │ …             │
//	│ light         │ …             │ …             │ …             │
//	├─────────lingo─┤ …             │ …             │ …             │
//	│ Pedantic      │ …             │ …             │ …             │
//	│ Funny         │ …             ╔═══════════════╗ …             │
//	│ Outrageous    │ …             ║ Blue on Blue  ║ …             │
//	│ …             │ …             ╚═══════════════╝ …             │
//	╰───────────────┴─ Tab list · arrows choose · Enter keep · Esc ─╯
//
// theme: dark, in-between or light backgrounds; lingo: the style of the
// names (Pedantic, Funny, …); palette: the theme's 36 pairs, each named in
// the lingo and drawn in its own colors, the chosen one in a double box
// (which covers the items above and below it in its column).
//
// The window shows each pair as it is chosen. Enter keeps it, Esc (or
// Alt+t again) puts the window's colors back. Delete chooses no palette:
// the terminal's own colors. Tab / Shift+Tab move between the lists; in a
// list ↑ ↓ choose, in the palette ← → too (← from the first column goes
// to the lingo list). A click chooses; a double click on a palette keeps
// it.
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
	list   int // the list with the keys: 0 theme, 1 lingo, 2 palette
	// The window's colors when the dialog opened (Esc puts them back).
	bg, fg color.Color
	pal    int
	// Where things were drawn, for clicks.
	themeRows, lingoRows []uv.Rectangle
	cells                []uv.Rectangle // the palette items, by position
	box                  uv.Rectangle
	// The last click on a palette item, for double clicks.
	clickAt  time.Time
	clickPos int
}

// Dialog geometry: the left column's inside width, the palette's rows and
// columns, and how wide a palette item may get.
const (
	palLeftW   = 15
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
	d := &paletteDialog{target: w, lingo: m.lingo, list: 2, bg: w.bgColor, fg: w.fgColor, pal: w.pal, pos: -1, clickPos: -1}
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
	case k.Code == uv.KeyTab && k.Mod.Contains(uv.ModShift):
		d.list = (d.list + 2) % 3
	case k.Code == uv.KeyTab:
		d.list = (d.list + 1) % 3
	case k.Code == uv.KeyDelete || k.Code == uv.KeyBackspace:
		d.pos = -1
		m.palPreview()
	case d.list == 0: // theme
		switch k.Code {
		case uv.KeyUp:
			m.palTheme(d.theme - 1)
		case uv.KeyDown:
			m.palTheme(d.theme + 1)
		case uv.KeyRight:
			d.list = 2
		}
	case d.list == 1: // lingo
		switch k.Code {
		case uv.KeyUp:
			d.lingo = max(d.lingo-1, 0)
		case uv.KeyDown:
			d.lingo = min(d.lingo+1, len(comboLingos)-1)
		case uv.KeyRight:
			d.list = 2
		}
	default: // palette: down the columns, one column after another
		p := d.pos
		switch k.Code {
		case uv.KeyUp:
			p--
		case uv.KeyDown:
			p++
		case uv.KeyLeft:
			if p < palRows { // the first column: on to the lingo list
				d.list = 1
				m.dirty = true
				return
			}
			p -= palRows
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
	for i, r := range d.themeRows {
		if p.In(r) {
			d.list = 0
			m.palTheme(i)
		}
	}
	for i, r := range d.lingoRows {
		if p.In(r) {
			d.list, d.lingo = 1, i
		}
	}
	for i, r := range d.cells {
		if !p.In(r) {
			continue
		}
		now := time.Now()
		double := d.clickPos == i && now.Sub(d.clickAt) < multiClickTime
		d.clickAt, d.clickPos = now, i
		d.list = 2
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
	dim := border
	dim.Attrs &^= uv.AttrBold
	dim.Fg = hint.Fg
	itemW := clamp((m.cols-palLeftW-3-palCols)/palCols, palItemMin, palItemMax)
	width := 1 + palLeftW + 1 + palCols*itemW + (palCols - 1) + 1
	height := palRows + 2
	x0, y0 := max((m.cols-width)/2, 0), max((m.areaH()-height)/2, 0)
	xL := x0 + 1 + palLeftW // the line between the lists and the palette
	x1, y1 := x0+width-1, y0+height-1
	d.box = uv.Rect(x0, y0, width, height)

	// The box, its column lines, the lingo separator.
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			put(scr, x, y, runeCell(' ', base))
		}
	}
	colX := func(c int) int { return xL + c*(itemW+1) } // the line left of palette column c
	for x := x0; x <= x1; x++ {
		put(scr, x, y0, runeCell(frameH, border))
		put(scr, x, y1, runeCell(frameH, border))
	}
	for y := y0; y <= y1; y++ {
		put(scr, x0, y, runeCell(frameV, border))
		put(scr, x1, y, runeCell(frameV, border))
		put(scr, xL, y, runeCell(frameV, border))
		for c := 1; c < palCols; c++ {
			put(scr, colX(c), y, runeCell(frameV, dim))
		}
	}
	put(scr, x0, y0, runeCell(frameTopLeft, border))
	put(scr, x1, y0, runeCell(frameTopRight, border))
	put(scr, x0, y1, runeCell(dialogBottomLeft, border))
	put(scr, x1, y1, runeCell(dialogBottomRight, border))
	put(scr, xL, y0, runeCell(dialogTeeDown, border))
	put(scr, xL, y1, runeCell(dialogTeeUp, border))
	for c := 1; c < palCols; c++ {
		put(scr, colX(c), y0, runeCell(dialogTeeDown, dim))
		put(scr, colX(c), y1, runeCell(dialogTeeUp, dim))
	}
	ySep := y0 + 1 + len(comboThemes) // "├───lingo─┤"
	put(scr, x0, ySep, runeCell(frameTeeLeft, border))
	for x := x0 + 1; x < xL; x++ {
		put(scr, x, ySep, runeCell(frameH, border))
	}
	put(scr, xL, ySep, runeCell(frameTeeRight, border))

	// List titles, right-aligned in their lines; the list with the keys
	// in the border's color, the others dimmer.
	title := func(text string, right, y int, active bool) {
		st := dim
		if active {
			st = border
		}
		cs := textCells(text, st, right-x0-2)
		putCells(scr, right-cellsWidth(cs)-1, y, cs)
	}
	title("theme", xL, y0, d.list == 0)
	title("lingo", xL, ySep, d.list == 1)
	title("palette", x1, y0, d.list == 2)

	// The theme and lingo lists: the chosen item reversed.
	item := func(text string, y int, chosen, active bool) uv.Rectangle {
		st := base
		if chosen {
			st.Attrs |= uv.AttrReverse
			if !active {
				st.Attrs |= uv.AttrFaint
			}
		}
		for x := x0 + 1; x < xL; x++ {
			put(scr, x, y, runeCell(' ', st))
		}
		putCells(scr, x0+2, y, textCells(text, st, palLeftW-2))
		return uv.Rect(x0+1, y, palLeftW, 1)
	}
	d.themeRows = d.themeRows[:0]
	for i, t := range comboThemeShown {
		d.themeRows = append(d.themeRows, item(t, y0+1+i, i == d.theme, d.list == 0))
	}
	d.lingoRows = d.lingoRows[:0]
	for i, l := range comboLingos {
		d.lingoRows = append(d.lingoRows, item(l, ySep+1+i, i == d.lingo, d.list == 1))
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
	// The chosen one's double box, over the items above and below it.
	if d.pos >= 0 && d.pos < len(list) {
		col, row := d.pos/palRows, d.pos%palRows
		l, r := colX(col), colX(col)+itemW+1
		y := y0 + 1 + row
		st := border
		if d.list != 2 {
			st = dim
			st.Attrs |= uv.AttrBold
		}
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

	// What to press, in the bottom line under the palette; the window's
	// name in the top line.
	help := " Tab list · arrows choose · Enter keep · Esc cancel · Del none "
	if cs := textCells(fitStart(help, x1-xL-3), hint, x1-xL-3); len(cs) > 0 {
		putCells(scr, x1-cellsWidth(cs)-1, y1, cs)
	}
	who := " " + m.tabName(d.target) + " "
	if cs := textCells(who, border, colX(palCols-1)-xL-3); len(cs) > 0 {
		putCells(scr, xL+2, y0, cs)
	}
}
