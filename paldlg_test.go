package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// paletteWM is a WM with palettes and a wide screen.
func paletteWM(t *testing.T) (*WM, *Window, *Window, *Window) {
	t.Helper()
	m, bg, a, b := wmWithBackground(t)
	m.cols, m.rows = 130, 30
	m.combos = testCombos(t)
	m.localPath = filepath.Join(t.TempDir(), "local.toml")
	return m, bg, a, b
}

func TestPaletteDialogLayout(t *testing.T) {
	m, _, a, _ := paletteWM(t)
	m.focus(a)
	key(m, uv.Key{Code: 't', Mod: uv.ModAlt})
	if m.palDlg == nil || m.palDlg.target != a {
		t.Fatal("Alt+t didn't open the palettes for the active window")
	}
	key(m, uv.Key{Code: uv.KeyDown}) // the first dark palette
	screen := settingsScreen(m)
	dark := m.combos.byTheme[0]
	for _, want := range []string{"palette", "theme: dark", "lingo: Pedantic",
		"╔", "╝", "║ " + m.combos.all[dark[0]].names[0], "Enter keep",
		m.combos.all[dark[12]].names[0], m.combos.all[dark[35]].names[0]} {
		if !strings.Contains(screen, want) {
			t.Errorf("dialog lacks %q:\n%s", want, screen)
		}
	}
	var inBox strings.Builder // the dialog alone (the window's own title shows behind it)
	{
		scr := uv.NewScreen(m.cols, m.rows)
		scene{m}.Draw(scr, scr.Bounds())
		bx := m.palDlg.box
		for y := bx.Min.Y; y < bx.Max.Y; y++ {
			for x := bx.Min.X; x < bx.Max.X; x++ {
				if c := scr.CellAt(x, y); c != nil {
					inBox.WriteString(c.Content)
				}
			}
			inBox.WriteByte('\n')
		}
	}
	for _, not := range []string{"in-between", "Funny", "Tab", "Del none", m.tabName(a)} {
		if strings.Contains(inBox.String(), not) {
			t.Errorf("dialog shows %q:\n%s", not, inBox.String())
		}
	}
	// Each palette is drawn in its own colors.
	scr := uv.NewScreen(m.cols, m.rows)
	scene{m}.Draw(scr, scr.Bounds())
	r := m.palDlg.cells[13]
	c := m.combos.get(dark[13])
	if cell := scr.CellAt(r.Min.X+1, r.Min.Y); cell == nil || cell.Style.Bg != m.combos.bg(c) || cell.Style.Fg != m.combos.fg(c) {
		t.Errorf("item 14 colors: %+v", cell)
	}
	// The chosen one's double box is in its colors; the column lines in
	// the frame's.
	ch := m.combos.get(dark[0])
	box := m.palDlg.cells[0]
	if cell := scr.CellAt(box.Min.X-1, box.Min.Y); cell == nil || cell.Content != "║" ||
		cell.Style.Bg != m.combos.bg(ch) || cell.Style.Fg != m.combos.fg(ch) {
		t.Errorf("double box: %+v", cell)
	}
	_, border, _ := m.dialogStyles()
	line := m.palDlg.cells[13]
	if cell := scr.CellAt(line.Min.X-1, line.Min.Y+3); cell == nil || cell.Content != "│" || cell.Style.Fg != border.Fg {
		t.Errorf("column line: %+v", cell)
	}
	// l: the next lingo renames them.
	key(m, uv.Key{Code: 'l', Text: "l"})
	if screen := settingsScreen(m); !strings.Contains(screen, m.combos.all[dark[0]].names[1]) || !strings.Contains(screen, "lingo: Funny") {
		t.Errorf("Funny names not shown:\n%s", screen)
	}
}

func TestPaletteDialogChooseAndKeep(t *testing.T) {
	m, _, a, b := paletteWM(t)
	m.focus(a)
	m.openPalette()
	before := len(a.in.buf)
	dark := m.combos.byTheme[0]
	key(m, uv.Key{Code: uv.KeyDown})  // 1st
	key(m, uv.Key{Code: uv.KeyDown})  // 2nd
	key(m, uv.Key{Code: uv.KeyRight}) // 14th (the next column)
	if a.pal != dark[13]+1 || a.bgColor != ansi.IndexedColor(m.combos.all[dark[13]].bgANSI) {
		t.Fatalf("preview: palette %d, bg %v", a.pal, a.bgColor)
	}
	if len(a.in.buf) != before {
		t.Error("keys reached the window")
	}
	// Another theme (t): the same place in its list.
	key(m, uv.Key{Code: 't', Text: "t"})
	if a.pal != m.combos.byTheme[1][13]+1 {
		t.Fatalf("in-between: palette %d", a.pal)
	}
	// The lingo (l): Funny, kept in local.toml.
	key(m, uv.Key{Code: 'l', Text: "l"})
	key(m, uv.Key{Code: uv.KeyEnter})
	if m.palDlg != nil || a.pal != m.combos.byTheme[1][13]+1 || m.lingo != 1 {
		t.Fatalf("Enter: open %v, palette %d, lingo %d", m.palDlg != nil, a.pal, m.lingo)
	}
	if data, _ := os.ReadFile(m.localPath); !strings.Contains(string(data), `palette_lingo = "Funny"`) {
		t.Errorf("lingo not saved:\n%s", data)
	}
	if !strings.Contains(m.barMsg, m.combos.all[m.combos.byTheme[1][13]].names[1]) {
		t.Errorf("status %q", m.barMsg)
	}

	// Esc puts the colors back.
	m.focus(b)
	b.bgColor, b.fgColor = ansi.IndexedColor(22), ansi.IndexedColor(230)
	m.openPalette()
	key(m, uv.Key{Code: uv.KeyDown})
	if b.pal == 0 {
		t.Fatal("no preview")
	}
	key(m, uv.Key{Code: uv.KeyEscape})
	if b.pal != 0 || b.bgColor != ansi.IndexedColor(22) || b.fgColor != ansi.IndexedColor(230) {
		t.Fatalf("Esc: palette %d, bg %v, fg %v", b.pal, b.bgColor, b.fgColor)
	}

	// Delete: the terminal's own colors.
	m.focus(a)
	m.openPalette()
	if m.palDlg.theme != 1 || m.palDlg.pos != 13 {
		t.Fatalf("opened at theme %d, position %d", m.palDlg.theme, m.palDlg.pos)
	}
	key(m, uv.Key{Code: uv.KeyDelete})
	key(m, uv.Key{Code: uv.KeyEnter})
	if a.pal != 0 || a.bgColor != nil || a.fgColor != nil {
		t.Fatalf("Delete: palette %d, bg %v", a.pal, a.bgColor)
	}
}

// A click on the theme or lingo goes to the next one (Shift+t, Shift+l:
// back, round); clicks choose a palette; a double click keeps it.
func TestPaletteDialogMouseAndKeys(t *testing.T) {
	m, _, a, _ := paletteWM(t)
	m.focus(a)
	m.openPalette()
	key(m, uv.Key{Code: 'T', Mod: uv.ModShift, Text: "T"}) // dark, back: light
	settingsScreen(m)
	if m.palDlg.theme != 2 {
		t.Fatalf("Shift+t: theme %d", m.palDlg.theme)
	}
	r := m.palDlg.themeAt
	m.handleMouse(uv.MouseClickEvent{X: r.Min.X + 1, Y: r.Min.Y, Button: uv.MouseLeft}) // light, on: dark
	if m.palDlg.theme != 0 {
		t.Fatalf("theme click: %d", m.palDlg.theme)
	}
	settingsScreen(m)
	r = m.palDlg.lingoAt
	m.handleMouse(uv.MouseClickEvent{X: r.Max.X - 2, Y: r.Min.Y, Button: uv.MouseLeft})
	if m.palDlg.lingo != 1 {
		t.Fatalf("lingo click: %d", m.palDlg.lingo)
	}
	m.palTheme(2)
	settingsScreen(m)
	c := m.palDlg.cells[30]
	click := uv.MouseClickEvent{X: c.Min.X + 2, Y: c.Min.Y, Button: uv.MouseLeft}
	m.handleMouse(click)
	if a.pal != m.combos.byTheme[2][30]+1 || m.palDlg == nil {
		t.Fatalf("click: palette %d", a.pal)
	}
	m.handleMouse(click)
	if m.palDlg != nil || a.pal != m.combos.byTheme[2][30]+1 {
		t.Fatal("double click didn't keep it")
	}
}

func TestPaletteDialogWithoutPalettes(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	key(m, uv.Key{Code: 't', Mod: uv.ModAlt})
	if m.palDlg != nil || !strings.Contains(m.barMsg, "goat -install") {
		t.Fatalf("no palettes: open %v, status %q", m.palDlg != nil, m.barMsg)
	}
	// Alt+b and Alt+f are no longer goat's.
	if DefaultConfig().bindings['b'] != actNone || DefaultConfig().bindings['f'] != actNone {
		t.Error("Alt+b / Alt+f still bound")
	}
}

// A new window takes the next background of its opener's theme.
func TestNewWindowNextPalette(t *testing.T) {
	m, _, a, _ := paletteWM(t)
	dark := m.combos.byTheme[0]
	m.setCombo(a, dark[4])
	w := fakeWindow(9, 0, 0, 20, 6, true, false)
	m.nextColorsFrom(a, w)
	if w.pal != dark[7]+1 {
		t.Fatalf("next: %d, want %d", w.pal-1, dark[7])
	}
	sameColorsAs(a, w)
	if w.pal != a.pal || w.bgColor != a.bgColor {
		t.Fatal("same colors")
	}
}

// The quit shortcut isn't held up by the dialog: it closes it (colors
// put back) and quits, as anywhere else.
func TestPaletteDialogQuit(t *testing.T) {
	m, _, a, _ := paletteWM(t)
	m.focus(a)
	m.openPalette()
	key(m, uv.Key{Code: uv.KeyDown}) // a preview
	key(m, uv.Key{Code: 'q', Mod: uv.ModAlt})
	if m.palDlg != nil || a.pal != 0 {
		t.Fatalf("dialog still open %v, palette %d", m.palDlg != nil, a.pal)
	}
	if !m.quit && m.quitAsk == nil {
		t.Fatal("Alt+q neither quit nor asked")
	}
}

func TestSettingsDialogQuit(t *testing.T) {
	m, _, a, _ := paletteWM(t)
	m.focus(a)
	_ = m.run(actSettings)
	if m.settings == nil {
		t.Fatal("no settings dialog")
	}
	key(m, uv.Key{Code: 'q', Mod: uv.ModAlt})
	if m.settings != nil || (!m.quit && m.quitAsk == nil) {
		t.Fatalf("settings open %v, quit %v, asked %v", m.settings != nil, m.quit, m.quitAsk != nil)
	}
}

// The chosen palette's double box is drawn over the dialog's frame (and the
// bottom line's text) where it reaches it: the top row, the bottom row.
func TestPaletteBoxOverFrame(t *testing.T) {
	m, _, a, _ := paletteWM(t)
	m.focus(a)
	m.openPalette()
	for _, pos := range []int{palRows - 1, 2*palRows + 0} { // bottom of column 1, top of column 3
		m.palDlg.pos = pos
		scr := uv.NewScreen(m.cols, m.rows)
		scene{m}.Draw(scr, scr.Bounds())
		r := m.palDlg.cells[pos]
		y := r.Min.Y + 1 // the box's bottom edge
		if pos%palRows == 0 {
			y = r.Min.Y - 1 // its top edge
		}
		for x := r.Min.X - 1; x <= r.Max.X; x++ {
			if c := scr.CellAt(x, y); c == nil || !strings.ContainsAny(c.Content, "═╔╗╚╝") {
				t.Fatalf("position %d: frame over the box at %d,%d: %+v", pos, x, y, c)
			}
		}
	}
}
