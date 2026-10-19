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
	for _, want := range []string{"theme", "lingo", "palette", "dark", "in-between", "light",
		"Pedantic", "Funny", "Outrageous", "Vulgar", "Corporate", "Geeky", "Wholesome", "Sports",
		"╔", "╝", "║ " + m.combos.all[dark[0]].names[0], "Enter keep",
		m.combos.all[dark[12]].names[0], m.combos.all[dark[35]].names[0]} {
		if !strings.Contains(screen, want) {
			t.Errorf("dialog lacks %q:\n%s", want, screen)
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
	// The lingo renames them.
	key(m, uv.Key{Code: uv.KeyTab}) // theme
	key(m, uv.Key{Code: uv.KeyTab}) // lingo
	key(m, uv.Key{Code: uv.KeyDown})
	if screen := settingsScreen(m); !strings.Contains(screen, m.combos.all[dark[0]].names[1]) {
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
	// Another theme: the same place in its list.
	key(m, uv.Key{Code: uv.KeyTab}) // theme
	key(m, uv.Key{Code: uv.KeyDown})
	if a.pal != m.combos.byTheme[1][13]+1 {
		t.Fatalf("in-between: palette %d", a.pal)
	}
	// The lingo: Funny, kept in local.toml.
	key(m, uv.Key{Code: uv.KeyTab}) // lingo
	key(m, uv.Key{Code: uv.KeyDown})
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

// ← from the first column goes to the lingo list; clicks choose; a double
// click keeps.
func TestPaletteDialogMouseAndKeys(t *testing.T) {
	m, _, a, _ := paletteWM(t)
	m.focus(a)
	m.openPalette()
	settingsScreen(m)
	key(m, uv.Key{Code: uv.KeyLeft})
	if m.palDlg.list != 1 {
		t.Fatalf("← went to list %d", m.palDlg.list)
	}
	r := m.palDlg.themeRows[2] // light
	m.handleMouse(uv.MouseClickEvent{X: r.Min.X + 1, Y: r.Min.Y, Button: uv.MouseLeft})
	if m.palDlg.theme != 2 || m.palDlg.list != 0 {
		t.Fatalf("theme click: %d (list %d)", m.palDlg.theme, m.palDlg.list)
	}
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
