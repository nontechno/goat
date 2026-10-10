package main

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// writePalette puts the given files into a temporary config directory.
func writePalette(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const (
	darkBg  = `{"role":"background","tone":"dark","colors":{"Zed":{"ansi":16},"Alpha":{"ansi":17},"Mid":{"hex":"#5f0000"}}}`
	lightBg = `{"role":"background","tone":"light","colors":{"Snow":{"ansi":231},"Ivory":{"ansi":230}}}`
	darkFg  = `{"role":"foreground","tone":"dark","colors":{"Bone":{"ansi":255},"Gold":{"ansi":220}}}`
	lightFg = `{"role":"foreground","tone":"light","colors":{"Ink":{"ansi":233},"Crimson":{"ansi":124},"Teal":{"ansi":30}}}`
)

func fullPalette(t *testing.T) *palette {
	t.Helper()
	p, warns := loadPalette(writePalette(t, map[string]string{
		"dark-background.color": darkBg, "light-background.color": lightBg,
		"dark-foreground.color": darkFg, "light-foreground.color": lightFg,
	}))
	if p == nil || len(warns) != 0 {
		t.Fatalf("palette %v, warnings %v", p, warns)
	}
	return p
}

func TestPaletteRealFiles(t *testing.T) {
	if _, err := os.Stat("colors/dark-background.color"); err != nil {
		t.Skip("no colors/ folder")
	}
	p, warns := loadPalette("colors")
	if p == nil || len(warns) != 0 {
		t.Fatalf("palette %v, warnings %v", p, warns)
	}
	if len(p.bgs) != 28 || len(p.fgs[toneDark]) != 16 || len(p.fgs[toneLight]) != 16 {
		t.Fatalf("counts: %d backgrounds, %d/%d text", len(p.bgs), len(p.fgs[toneDark]), len(p.fgs[toneLight]))
	}
	// File order, dark then light.
	if p.bgs[0].name != "Obsidian" || p.bgs[13].name != "Umber" || p.bgs[14].name != "Snow" || p.bgs[27].name != "Sage" {
		t.Fatalf("order: %s %s %s %s", p.bgs[0].name, p.bgs[13].name, p.bgs[14].name, p.bgs[27].name)
	}
	if p.bgs[2].c != ansi.IndexedColor(17) || p.bgs[14].tone != toneLight {
		t.Fatalf("Midnight %v, Snow tone %v", p.bgs[2].c, p.bgs[14].tone)
	}
}

func TestPaletteProblems(t *testing.T) {
	if p, w := loadPalette(t.TempDir()); p != nil || w != nil {
		t.Fatal("no files: no palette, no warnings")
	}
	p, warns := loadPalette(writePalette(t, map[string]string{
		"dark-background.color":  darkBg,
		"light-background.color": `{"role":"foreground","tone":"light","colors":{"X":{"ansi":1}}}`, // wrong role
		"dark-foreground.color":  `{not json`,
	}))
	if p == nil || len(p.bgs) != 3 {
		t.Fatalf("good file not used: %+v", p)
	}
	all := strings.Join(warns, "\n")
	for _, want := range []string{"light-background.color", `"role" is "foreground"`, "dark-foreground.color", "only 3 of the 4"} {
		if !strings.Contains(all, want) {
			t.Fatalf("warnings lack %q:\n%s", want, all)
		}
	}
	if _, err := parseColorFile([]byte(`{"role":"background","tone":"dark","colors":{"A":{"ansi":300}}}`), "background", toneDark); err == nil {
		t.Fatal("ansi 300 accepted")
	}
}

func TestAltBAndAltF(t *testing.T) {
	m, _, _, w := wmWithBackground(t)
	m.palette = fullPalette(t)
	m.hostBg = ansi.IndexedColor(16) // a dark terminal
	key := func(r rune) { _ = m.handleKey(uv.Key{Code: r, Text: string(r), Mod: uv.ModAlt}) }
	ink, crimson, teal, bone := ansi.IndexedColor(233), ansi.IndexedColor(124), ansi.IndexedColor(30), ansi.IndexedColor(255)

	// Alt+b: dark backgrounds in file order, then light, then the terminal's.
	// The terminal's (light) text doesn't show on the light ones: it becomes
	// the first light-background text color.
	for i, step := range []struct {
		name    string
		fg      any
		changed bool
	}{
		{"Zed", nil, false}, {"Alpha", nil, false}, {"Mid", nil, false},
		{"Snow", ink, true}, {"Ivory", ink, false},
	} {
		key('b')
		if w.bgPick != i+1 || !strings.Contains(m.status, step.name) || anyColor(w.fgColor) != step.fg ||
			strings.Contains(m.status, "changed") != step.changed {
			t.Fatalf("Alt+b #%d: index %d, status %q, fg %v", i+1, w.bgPick, m.status, w.fgColor)
		}
	}
	key('b') // the terminal's (dark) background: Ink wouldn't show
	if w.bgPick != 0 || w.bgColor != nil || w.fgColor != bone {
		t.Fatalf("after the last background: %d %v fg %v", w.bgPick, w.bgColor, w.fgColor)
	}

	// Alt+f on a light background: the light-background text colors.
	for range 4 {
		key('b') // Zed, Alpha, Mid, Snow
	}
	if w.fgColor != ink {
		t.Fatalf("Bone on Snow not fixed: %v", w.fgColor)
	}
	key('f')
	if w.fgColor != crimson || !strings.Contains(m.status, "Crimson") {
		t.Fatalf("Alt+f on Snow: %v %q", w.fgColor, m.status)
	}
	key('f')
	if w.fgColor != teal {
		t.Fatalf("second Alt+f: %v", w.fgColor)
	}
	// Past the last one comes the terminal's text color, which doesn't
	// show on Snow: skipped, back to the first.
	key('f')
	if w.fgColor != ink || !strings.Contains(m.status, "skipped 1") {
		t.Fatalf("unreadable terminal color not skipped: %v %q", w.fgColor, m.status)
	}
	key('f')
	key('f') // Teal again
	// Alt+b keeps a readable text color, and says when it's for the other tone.
	key('b') // Ivory (light): fine
	if w.fgColor != teal || strings.Contains(m.status, "Alt+f") {
		t.Fatalf("Alt+b changed text or warned: %v %q", w.fgColor, m.status)
	}
	key('b') // the terminal's background (dark): Teal still shows
	key('b') // Zed (dark)
	if w.fgColor != teal || !strings.Contains(m.status, "Alt+f") {
		t.Fatalf("tone change not mentioned: %v %q", w.fgColor, m.status)
	}
	// Alt+f now goes through the dark-background text colors from the start.
	key('f')
	if w.fgColor != bone {
		t.Fatalf("Alt+f after tone change: %v", w.fgColor)
	}
	key('f')
	key('f') // past Gold: the terminal's text color
	if w.fgColor != nil || w.fgPick != 0 {
		t.Fatalf("after the last text color: %v", w.fgColor)
	}
}

// anyColor makes a nil color.Color compare equal to an untyped nil.
func anyColor(c any) any { return c }

// With the check off (min_text_contrast = 0), any pair goes.
func TestTextContrastOff(t *testing.T) {
	m, _, _, w := wmWithBackground(t)
	m.palette = fullPalette(t)
	m.cfg.Theme.MinTextContrast = 0
	for range 4 {
		_ = m.run(actCycleBackground) // to Snow
	}
	if w.fgColor != nil {
		t.Fatalf("text color changed with the check off: %v", w.fgColor)
	}
}

func TestTerminalBackgroundTone(t *testing.T) {
	m, _, _, w := wmWithBackground(t)
	m.palette = fullPalette(t)
	if m.backgroundTone(w) != toneDark {
		t.Fatal("unknown terminal background should count as dark")
	}
	m.hostBg = ansi.IndexedColor(231) // a light terminal
	_ = m.handleKey(uv.Key{Code: 'f', Text: "f", Mod: uv.ModAlt})
	if w.fgColor != ansi.IndexedColor(233) {
		t.Fatalf("light terminal: text %v, want the first light-background one", w.fgColor)
	}
}

func TestNoPaletteFallsBack(t *testing.T) {
	m, _, _, w := wmWithBackground(t)
	_ = m.handleKey(uv.Key{Code: 'b', Text: "b", Mod: uv.ModAlt})
	if w.bgColor != nil || !strings.Contains(m.status, "goat -install") {
		t.Fatalf("Alt+b without files: %v %q", w.bgColor, m.status)
	}
	_ = m.handleKey(uv.Key{Code: 'f', Text: "f", Mod: uv.ModAlt})
	if !strings.Contains(m.status, "goat -install") {
		t.Fatalf("Alt+f without files: %q", m.status)
	}
}

func TestNewWindowNextPaletteBackground(t *testing.T) {
	m, _, _, from := wmWithBackground(t)
	m.palette = fullPalette(t)
	from.bgPick, from.bgColor = 3, m.palette.bgs[2].c // Mid (dark)
	from.fgPick, from.fgTone, from.fgColor = 2, toneDark, ansi.IndexedColor(220)
	w := fakeWindow(9, 0, 0, 20, 6, true, false)
	m.initBackground(w)
	m.nextColorsFrom(from, w)
	if w.bgPick != 4 || w.fgTone != toneLight || w.fgColor != ansi.IndexedColor(233) {
		t.Fatalf("next: bg %d, fg %v (%v)", w.bgPick, w.fgColor, w.fgTone)
	}
	from.bgPick, from.bgColor = 1, m.palette.bgs[0].c // Zed
	m.nextColorsFrom(from, w) // dark to dark: from's text color carries over
	if w.bgPick != 2 || w.fgColor != ansi.IndexedColor(220) {
		t.Fatalf("same tone: bg %d, fg %v", w.bgPick, w.fgColor)
	}
}

// Alt+c: with new_window_next_colors off, the new window gets the active
// window's colors; on, the next background.
func TestNewWindowColorsFromActive(t *testing.T) {
	for _, next := range []bool{false, true} {
		cfg := DefaultConfig()
		cfg.Shell = "/bin/sh"
		cfg.Theme.NewWindowNextColors = next
		m := newWM(cfg, nil, 100, 30)
		m.palette = fullPalette(t)
		done := make(chan struct{})
		go func() {
			for {
				select {
				case <-m.out:
				case <-done:
					return
				}
			}
		}()
		if err := m.newWindow(); err != nil {
			t.Fatal(err)
		}
		from := m.focused
		from.bgPick, from.bgColor = 3, m.palette.bgs[2].c
		from.fgPick, from.fgTone, from.fgColor = 2, toneDark, ansi.IndexedColor(220)
		if err := m.newWindow(); err != nil {
			t.Fatal(err)
		}
		w := m.focused
		same := w.bgColor == from.bgColor && w.fgColor == from.fgColor &&
			w.bgPick == from.bgPick && w.fgPick == from.fgPick && w.fgTone == from.fgTone
		if w == from || same == next {
			t.Errorf("next=%v: new window bg %d %v fg %d %v", next, w.bgPick, w.bgColor, w.fgPick, w.fgColor)
		}
		m.closeAll()
		close(done)
	}
}

// A background in both lists is kept only in the dark one, and one listed
// twice in a list only the first time (the same color counts, whether given
// by palette number or hex).
func TestLightBackgroundDuplicatesDropped(t *testing.T) {
	p, warns := loadPalette(writePalette(t, map[string]string{
		"dark-background.color":  `{"role":"background","tone":"dark","colors":{"Navy":{"ansi":17},"Grey":{"ansi":245},"Navy hex":{"hex":"#00005f"}}}`,
		"light-background.color": `{"role":"background","tone":"light","colors":{"Snow":{"ansi":231},"Grey too":{"hex":"#8a8a8a"},"Navy again":{"ansi":17}}}`,
		"dark-foreground.color":  darkFg, "light-foreground.color": lightFg,
	}))
	if p == nil || len(warns) != 0 {
		t.Fatalf("palette %v, warnings %v", p, warns)
	}
	var names []string
	for _, c := range p.bgs {
		names = append(names, c.name+"/"+c.tone.String())
	}
	if got := strings.Join(names, " "); got != "Navy/dark Grey/dark Snow/light" {
		t.Fatalf("backgrounds: %s", got)
	}
}

// Alt+b goes on from where the window's background color is in the circle,
// whatever got it there (a swap, a restored color, an edit to the files).
func TestAltBFindsCurrentColor(t *testing.T) {
	m, _, _, w := wmWithBackground(t)
	m.palette = fullPalette(t) // Zed 16, Alpha 17, Mid, Snow 231, Ivory 230
	for _, tc := range []struct {
		bg   color.Color
		pick int // stale on purpose
		want string
	}{
		{ansi.IndexedColor(231), 0, "Ivory"},          // Snow -> Ivory
		{ansi.IndexedColor(230), 1, "terminal's own"}, // the last -> around
		{nil, 4, "Zed"},                   // the terminal's -> first
		{ansi.IndexedColor(17), 0, "Mid"}, // Alpha -> Mid
		{ansi.IndexedColor(99), 3, "Zed"}, // not listed -> first
	} {
		w.bgColor, w.bgPick = tc.bg, tc.pick
		_ = m.run(actCycleBackground)
		if !strings.Contains(m.status, tc.want) {
			t.Errorf("from %v: %q, want %s", tc.bg, m.status, tc.want)
		}
	}
}
