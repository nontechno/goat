package main

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if readLocalHostname(path) != "friendly" {
		t.Fatal("hostname no longer read")
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

// Alt+b / Alt+f on the background window save its colors; on a floating
// window they don't. A fresh background shell gets the saved colors back.
func TestDesktopColorsSavedAndRestored(t *testing.T) {
	m, bg, _, fw := wmWithBackground(t)
	m.palette = fullPalette(t)
	m.hostBg = ansi.IndexedColor(16)
	m.localPath = filepath.Join(t.TempDir(), "local.toml")

	m.focus(fw)
	_ = m.run(actCycleBackground)
	if _, err := os.Stat(m.localPath); !os.IsNotExist(err) {
		t.Fatal("a floating window's colors were saved")
	}

	m.focus(bg)
	_ = m.run(actCycleBackground) // Zed
	_ = m.run(actCycleBackground) // Alpha
	_ = m.run(actCycleForeground) // Bone
	d, ok, _ := readDesktopColors(m.localPath)
	if !ok || d.bg != ansi.IndexedColor(17) || d.fg != ansi.IndexedColor(255) {
		t.Fatalf("saved %+v ok %v", d, ok)
	}
	data, _ := os.ReadFile(m.localPath)
	if !strings.Contains(string(data), "# Alpha") || !strings.Contains(string(data), "# Bone") {
		t.Fatalf("names missing:\n%s", data)
	}

	// Nothing changed: no write.
	before, _ := os.Stat(m.localPath)
	_ = m.run(actMoveLeft)
	if after, _ := os.Stat(m.localPath); !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("rewritten without a change")
	}

	// Restored, with its place in the lists.
	w := fakeWindow(0, 0, 0, 20, 6, false, false)
	m.initBackground(w)
	m.restoreDesktop(w)
	if w.bgColor != ansi.IndexedColor(17) || w.bgPick != 2 || w.fgColor != ansi.IndexedColor(255) ||
		w.fgPick != 1 || w.fgTone != toneDark {
		t.Fatalf("restored bg %v (%d) fg %v (%d %v)", w.bgColor, w.bgPick, w.fgColor, w.fgPick, w.fgTone)
	}

	// Saved colors that wouldn't show together get a readable text color.
	m.desk = desktopColors{bg: ansi.IndexedColor(231), fg: ansi.IndexedColor(255)}
	m.restoreDesktop(w)
	if w.fgColor != ansi.IndexedColor(233) {
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
