package main

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const userLocal = `# goat local configuration file
# Location: ~/.config/goat/local.toml

# friendly hostname
hostname = "friendly"

# goat --learn
hotkey = ""
`

func TestDesktopColorsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.toml")
	if err := os.WriteFile(path, []byte(userLocal), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, w := readDesktopColors(path); ok || w != "" {
		t.Fatalf("no section yet: ok %v, %q", ok, w)
	}

	d := desktopColors{bg: ansi.IndexedColor(17), fg: ansi.IndexedColor(230)}
	if err := writeDesktopColors(path, d, [2]string{"Midnight", "Cream"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.HasPrefix(text, userLocal) {
		t.Fatalf("the user's lines changed:\n%s", text)
	}
	for _, want := range []string{"[background_window]", "bg = 17    # Midnight", "fg = 230   # Cream"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	got, ok, w := readDesktopColors(path)
	if !ok || w != "" || !got.same(d) {
		t.Fatalf("read back %+v ok %v %q", got, ok, w)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `hostname = "friendly"`) {
		t.Fatal("other lines of local.toml lost")
	}

	// Saving again replaces the section (once), keeping everything else;
	// the terminal's own color is "default", a 24-bit one "#rrggbb".
	d = desktopColors{bg: nil, fg: mustHex(t, "#102030")}
	if err := writeDesktopColors(path, d, [2]string{}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	text = string(data)
	if strings.Count(text, "[background_window]") != 1 || !strings.HasPrefix(text, userLocal) ||
		!strings.Contains(text, `bg = "default"`) || !strings.Contains(text, `fg = "#102030"`) {
		t.Fatalf("second save:\n%s", text)
	}
	if got, ok, _ := readDesktopColors(path); !ok || !got.same(d) {
		t.Fatalf("read back %+v", got)
	}
}

func mustHex(t *testing.T, s string) color.Color {
	t.Helper()
	c, err := parseHexColor(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A section in the middle of the file is replaced where it is.
func TestReplaceSectionInMiddle(t *testing.T) {
	in := "a = 1\n\n[background_window]\nbg = 1\nfg = 2\n\n[other]\nx = 3\n"
	got := replaceSection(strings.Split(in, "\n"), "background_window", []string{"[background_window]", "bg = 5"})
	want := "a = 1\n\n[background_window]\nbg = 5\n\n[other]\nx = 3\n"
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
	// No file yet: just the section.
	if got := replaceSection([]string{""}, "s", []string{"[s]", "k = 1"}); got != "[s]\nk = 1\n" {
		t.Fatalf("empty file: %q", got)
	}
}

// A palette kept for the background window is saved; one for a floating
// window isn't. A fresh background shell gets the saved colors back, and
// its palette.
func TestDesktopColorsSavedAndRestored(t *testing.T) {
	m, bg, _, fw := wmWithBackground(t)
	m.combos = testCombos(t)
	m.hostBg = ansi.IndexedColor(234)
	m.localPath = filepath.Join(t.TempDir(), "local.toml")

	m.focus(fw)
	_ = m.run(actPalette)
	key(m, uv.Key{Code: uv.KeyDown})
	key(m, uv.Key{Code: uv.KeyEnter})
	if fw.pal == 0 {
		t.Fatal("no palette chosen")
	}
	if _, err := os.Stat(m.localPath); !os.IsNotExist(err) {
		t.Fatal("a floating window's colors were saved")
	}

	m.focus(bg)
	_ = m.run(actPalette)
	key(m, uv.Key{Code: uv.KeyDown}) // (no palette yet) the first dark one
	key(m, uv.Key{Code: uv.KeyDown}) // the second
	key(m, uv.Key{Code: uv.KeyEnter})
	want := m.combos.get(m.combos.byTheme[0][1])
	d, ok, _ := readDesktopColors(m.localPath)
	if !ok || d.bg != ansi.IndexedColor(want.bgANSI) || d.fg != ansi.IndexedColor(want.fgANSI) {
		t.Fatalf("saved %+v ok %v", d, ok)
	}
	data, _ := os.ReadFile(m.localPath)
	if !strings.Contains(string(data), `palette = "`+want.names[0]+`"`) {
		t.Fatalf("name missing:\n%s", data)
	}

	// Nothing changed: no write.
	before, _ := os.Stat(m.localPath)
	_ = m.run(actMoveLeft)
	if after, _ := os.Stat(m.localPath); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("rewritten without a change")
	}

	// Restored, with its palette.
	w := fakeWindow(0, 0, 0, 20, 6, false, false)
	m.initBackground(w)
	m.restoreDesktop(w)
	if w.bgColor != ansi.IndexedColor(want.bgANSI) || w.pal != m.combos.byTheme[0][1]+1 {
		t.Fatalf("restored bg %v palette %d", w.bgColor, w.pal)
	}

	// Restored by the palette's id: with 24-bit colors on, its 24-bit
	// colors (not the 256 colors saved), still that palette.
	if d.pal != want.names[0] {
		t.Fatalf("palette saved: %q, want %q", d.pal, want.names[0])
	}
	m.combos.trueColor = true
	m.desk, _, _ = readDesktopColors(m.localPath)
	m.restoreDesktop(w)
	if !sameColor(w.bgColor, m.combos.bg(want)) || w.pal != m.combos.byTheme[0][1]+1 {
		t.Fatalf("restored by id: bg %v palette %d", w.bgColor, w.pal)
	}
	m.combos.trueColor = false
	// An id, as earlier versions wrote it, is read too.
	m.desk = desktopColors{pal: fmt.Sprint(want.id)}
	w.pal = 0
	m.restoreDesktop(w)
	if c := m.combos.get(w.pal - 1); c == nil || c.id != want.id {
		t.Fatalf("restored by an old id: palette %d", w.pal)
	}

	// Saved colors that wouldn't show together get a readable text color.
	m.desk = desktopColors{bg: ansi.IndexedColor(231), fg: ansi.IndexedColor(255)}
	m.restoreDesktop(w)
	if !m.readable(w.fgColor, w.bgColor) || w.pal != 0 {
		t.Fatalf("white on white restored: %v", w.fgColor)
	}
}

// Alt+s puts another window's colors on the background: saved too.
func TestDesktopColorsSavedOnSwap(t *testing.T) {
	m, _, _, fw := wmWithBackground(t)
	m.localPath = filepath.Join(t.TempDir(), "local.toml")
	fw.bgColor, fw.fgColor = ansi.IndexedColor(22), ansi.IndexedColor(230)
	m.focus(fw)
	_ = m.run(actSwapBackground)
	if d, ok, _ := readDesktopColors(m.localPath); !ok || d.bg != ansi.IndexedColor(22) || d.fg != ansi.IndexedColor(230) {
		t.Fatalf("after swap: %+v %v", d, ok)
	}
}
