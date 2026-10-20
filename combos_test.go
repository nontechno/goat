package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// testCombos is the palettes file shipped in testdata (a copy of
// github.com/nontechno/color's).
func testCombos(t *testing.T) *comboSet {
	t.Helper()
	s, warns := loadCombos("testdata")
	if s == nil || len(warns) != 0 {
		t.Fatalf("palettes: %v %v", s, warns)
	}
	s.trueColor = false
	return s
}

func TestLoadCombos(t *testing.T) {
	s := testCombos(t)
	if len(s.all) != 108 {
		t.Fatalf("%d palettes", len(s.all))
	}
	for th, list := range s.byTheme {
		if len(list) != combosPerTheme {
			t.Errorf("theme %s: %d", comboThemes[th], len(list))
		}
	}
	c := s.get(0)
	if c.id != 29456 || c.names[0] != "Aquamarine on Black" || c.names[1] != "Minty Fresh Void" ||
		c.names[7] != "Jags Teal Blackout" || s.bg(c) != ansi.IndexedColor(16) || s.fg(c) != ansi.IndexedColor(115) {
		t.Errorf("first: %+v", c)
	}
	s.trueColor = true
	if !sameColor(s.fg(c), mustHex(t, "#7ddcc4")) {
		t.Errorf("true color: %v", s.fg(c))
	}
	// Found by id, by a name in any lingo.
	if s.find("29456") != 0 || s.find("minty fresh void") != 0 || s.find("Jags Teal Blackout") != 0 || s.find("nope") != -1 {
		t.Error("find")
	}
	// Next background in the theme, same text position.
	if n := s.nextCombo(0); n != s.byTheme[0][3] {
		t.Errorf("next: %d", n)
	}
	if n := s.nextCombo(s.byTheme[0][35]); n != s.byTheme[0][2] {
		t.Errorf("next wraps: %d", n)
	}
}

func TestCombosProblems(t *testing.T) {
	dir := t.TempDir()
	if s, w := loadCombos(dir); s != nil || w != nil {
		t.Errorf("no file: %v %v", s, w)
	}
	bad := `[{"Theme":"dark","Background.rgb":"#000000","Background.ansi":16,"Text.rgb":"#ffffff","Text.ansi":231,"Pedantic":"White on Black"},
	{"Theme":"purple","Background.rgb":"#000000","Background.ansi":16,"Text.rgb":"#ffffff","Text.ansi":231},
	{"Theme":"light","Background.rgb":"nope","Background.ansi":16,"Text.rgb":"#ffffff","Text.ansi":231}]`
	_ = os.WriteFile(filepath.Join(dir, combosFile), []byte(bad), 0o644)
	s, warns := loadCombos(dir)
	if s == nil || len(s.all) != 1 || len(warns) != 2 {
		t.Fatalf("%v %v", s, warns)
	}
	// Missing names fall back to the plain one; the id is computed.
	if c := s.get(0); c.names[3] != "White on Black" || c.id != 231*256+16 {
		t.Errorf("%+v", c)
	}
	_ = os.WriteFile(filepath.Join(dir, combosFile), []byte("<html>"), 0o644)
	if s, warns := loadCombos(dir); s != nil || len(warns) == 0 || !strings.Contains(warns[0], "not a list") {
		t.Errorf("html: %v %v", s, warns)
	}
}

// palette_colors: #RRGGBB by default; xterm-256 indexes when asked; "auto"
// follows true_color.
func TestPaletteColorsSetting(t *testing.T) {
	if c, w := parseConfig(""); len(w) != 0 || c.PaletteColors != "rgb" {
		t.Errorf("default %q %v", c.PaletteColors, w)
	}
	if c, w := parseConfig("palette_colors = \"hex\"\n"); len(w) != 1 || c.PaletteColors != "rgb" {
		t.Errorf("bad value: %q %v", c.PaletteColors, w)
	}
	for _, tc := range []struct {
		set       string
		trueColor bool
		want      bool
	}{{"rgb", false, true}, {"rgb", true, true}, {"ansi", true, false}, {"auto", true, true}, {"auto", false, false}} {
		if got := paletteRGB(tc.set, tc.trueColor); got != tc.want {
			t.Errorf("%s, 24-bit %v: %v", tc.set, tc.trueColor, got)
		}
	}
	s := testCombos(t)
	c := &s.all[0]
	s.trueColor = true
	if !sameColor(s.bg(c), c.bgRGB) {
		t.Error("rgb: not the #RRGGBB color")
	}
}
