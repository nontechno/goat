package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// Palettes: background + text color pairs for windows, from
// terminal-color-combinations.json in the config folder ("goat -install"
// fetches it; https://github.com/nontechno/color, usage.md there).
//
// The file has 108 pairs ("combinations"), 36 for each theme: dark,
// between (shown as "in-between") and light, which describes the
// background. Each has its colors as RGB and as the nearest xterm-256
// index, and a name in each of eight styles ("lingos"): Pedantic, Funny,
// Outrageous, Vulgar, Corporate, Geeky, Wholesome, Sports.
//
// Alt+t (keys.palette) chooses one for the active window (paldlg.go).
// A choice is stored by the pair's "id" (text index * 256 + background
// index). Ids aren't unique in the file: an id stands for the first pair
// that has it.

// combosFile is the file's name, in the config folder.
const combosFile = "terminal-color-combinations.json"

// comboThemes are the themes in the file, and their names in the dialog.
var (
	comboThemes     = []string{"dark", "between", "light"}
	comboThemeShown = []string{"dark", "in-between", "light"}
	comboLingos     = []string{"Pedantic", "Funny", "Outrageous", "Vulgar", "Corporate", "Geeky", "Wholesome", "Sports"}
)

// combosPerTheme is how many pairs each theme has (the dialog's grid).
const combosPerTheme = 36

// combo is one pair.
type combo struct {
	id         int
	theme      int // index into comboThemes
	luminosity int
	bgRGB      color.Color
	fgRGB      color.Color
	bgANSI     int
	fgANSI     int
	names      [8]string // by comboLingos
}

// comboSet is the file, read.
type comboSet struct {
	all     []combo
	byTheme [3][]int // indexes into all, in file order
	// trueColor: use the #RRGGBB colors, else the xterm-256 indexes (the
	// same everywhere, macOS Terminal included). config.toml's
	// palette_colors decides (paletteRGB); without 24-bit color the
	// renderer shows #RRGGBB as the nearest of the 256 colors.
	trueColor bool
}

// bg and fg are the pair's colors as goat draws them.
func (s *comboSet) bg(c *combo) color.Color {
	if s.trueColor {
		return c.bgRGB
	}
	return ansi.IndexedColor(c.bgANSI)
}

func (s *comboSet) fg(c *combo) color.Color {
	if s.trueColor {
		return c.fgRGB
	}
	return ansi.IndexedColor(c.fgANSI)
}

// get is pair i (0-based; nil if out of range or no set).
func (s *comboSet) get(i int) *combo {
	if s == nil || i < 0 || i >= len(s.all) {
		return nil
	}
	return &s.all[i]
}

// byID finds the first pair with an id (-1 if none).
func (s *comboSet) byID(id int) int {
	if s == nil {
		return -1
	}
	for i := range s.all {
		if s.all[i].id == id {
			return i
		}
	}
	return -1
}

// find finds a pair by its id, or its name in any lingo (any case); -1 if
// none.
func (s *comboSet) find(v string) int {
	if s == nil {
		return -1
	}
	v = strings.TrimSpace(v)
	if id, err := strconv.Atoi(v); err == nil {
		return s.byID(id)
	}
	for i := range s.all {
		for _, n := range s.all[i].names {
			if strings.EqualFold(n, v) {
				return i
			}
		}
	}
	return -1
}

// position is where pair i is in its theme's list (0-35; -1 if not there).
func (s *comboSet) position(i int) int {
	c := s.get(i)
	if c == nil {
		return -1
	}
	for p, j := range s.byTheme[c.theme] {
		if j == i {
			return p
		}
	}
	return -1
}

// lingoIndex is a lingo's index by name (any case; -1 if unknown).
func lingoIndex(name string) int {
	for i, l := range comboLingos {
		if strings.EqualFold(l, name) {
			return i
		}
	}
	return -1
}

// comboJSON is one entry of the file.
type comboJSON struct {
	ID             *int   `json:"id"`
	Theme          string `json:"Theme"`
	Luminosity     int    `json:"Luminosity"`
	BackgroundRGB  string `json:"Background.rgb"`
	BackgroundANSI *int   `json:"Background.ansi"`
	TextRGB        string `json:"Text.rgb"`
	TextANSI       *int   `json:"Text.ansi"`
	Pedantic       string `json:"Pedantic"`
	Funny          string `json:"Funny"`
	Outrageous     string `json:"Outrageous"`
	Vulgar         string `json:"Vulgar"`
	Corporate      string `json:"Corporate"`
	Geeky          string `json:"Geeky"`
	Wholesome      string `json:"Wholesome"`
	Sports         string `json:"Sports"`
}

// parseCombos reads the file's text. Entries that can't be used are left
// out (reported in the error-free warnings); a file without a full theme
// of usable entries is an error.
func parseCombos(data []byte) (*comboSet, []string, error) {
	var raw []comboJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("not a list of color combinations: %w", err)
	}
	s := &comboSet{}
	var warns []string
	for n, r := range raw {
		c, err := r.combo()
		if err != nil {
			warns = append(warns, fmt.Sprintf("entry %d: %v (left out)", n+1, err))
			continue
		}
		if len(s.byTheme[c.theme]) == combosPerTheme {
			warns = append(warns, fmt.Sprintf("entry %d: more than %d %s combinations (left out)", n+1, combosPerTheme, comboThemes[c.theme]))
			continue
		}
		s.byTheme[c.theme] = append(s.byTheme[c.theme], len(s.all))
		s.all = append(s.all, c)
	}
	if len(s.all) == 0 {
		return nil, warns, errors.New("no usable color combinations")
	}
	return s, warns, nil
}

func (r comboJSON) combo() (combo, error) {
	var c combo
	c.theme = -1
	for i, t := range comboThemes {
		if strings.EqualFold(r.Theme, t) {
			c.theme = i
		}
	}
	if c.theme < 0 {
		return c, fmt.Errorf("Theme %q: want dark, between or light", r.Theme)
	}
	var err error
	if c.bgRGB, err = parseHexColor(r.BackgroundRGB); err != nil {
		return c, fmt.Errorf("Background.rgb: %w", err)
	}
	if c.fgRGB, err = parseHexColor(r.TextRGB); err != nil {
		return c, fmt.Errorf("Text.rgb: %w", err)
	}
	okIndex := func(p *int) bool { return p != nil && *p >= 0 && *p <= 255 }
	if !okIndex(r.BackgroundANSI) || !okIndex(r.TextANSI) {
		return c, errors.New("Background.ansi and Text.ansi must be 0-255")
	}
	c.bgANSI, c.fgANSI = *r.BackgroundANSI, *r.TextANSI
	c.id = c.fgANSI*256 + c.bgANSI
	if r.ID != nil {
		c.id = *r.ID
	}
	c.luminosity = r.Luminosity
	c.names = [8]string{r.Pedantic, r.Funny, r.Outrageous, r.Vulgar, r.Corporate, r.Geeky, r.Wholesome, r.Sports}
	for i, n := range c.names {
		n = cleanName(n)
		if n == "" {
			n = r.Pedantic
		}
		if n == "" {
			n = fmt.Sprintf("#%d", c.id)
		}
		c.names[i] = n
	}
	return c, nil
}

// cleanName drops control and invisible characters from a name (it is
// drawn on the screen) and keeps it to 32 characters.
func cleanName(s string) string {
	s = strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > 32 {
		s = string(r[:32])
	}
	return strings.TrimSpace(s)
}

// loadCombos reads the file from dir. No file is not an error (nil set:
// Alt+t says to run goat -install).
func loadCombos(dir string) (*comboSet, []string) {
	path := filepath.Join(dir, combosFile)
	data, err := readSmallFile(path, 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: %v", path, err)}
	}
	s, warns, err := parseCombos(data)
	for i := range warns {
		warns[i] = path + ": " + warns[i]
	}
	if err != nil {
		return nil, append(warns, fmt.Sprintf("%s: %v (no palettes)", path, err))
	}
	return s, warns
}

// noCombos is the message for Alt+t without the file.
const noCombos = "no palettes: " + combosFile + " isn't in the config folder (goat -install fetches it)"

// ---- windows ----------------------------------------------------------------

// setCombo gives w pair i (-1: the terminal's own colors).
func (m *WM) setCombo(w *Window, i int) {
	c := m.combos.get(i)
	if c == nil {
		w.pal, w.bgColor, w.fgColor = 0, nil, nil
	} else {
		w.pal, w.bgColor, w.fgColor = i+1, m.combos.bg(c), m.combos.fg(c)
	}
	m.applyHostColors(w) // programs asking for the colors get these
	m.dirty = true
}

// comboName is pair i's name in the chosen lingo ("" for none).
func (m *WM) comboName(i int) string {
	c := m.combos.get(i)
	if c == nil {
		return ""
	}
	return c.names[max(m.lingo, 0)]
}

// findCombo finds the pair with these colors (-1 if none): how a window
// whose colors were saved as colors (the background window's, local.toml)
// gets its pair back.
func (m *WM) findCombo(bg, fg color.Color) int {
	s := m.combos
	if s == nil || bg == nil || fg == nil {
		return -1
	}
	for i := range s.all {
		c := &s.all[i]
		if sameColor(bg, s.bg(c)) && sameColor(fg, s.fg(c)) {
			return i
		}
	}
	for i := range s.all { // saved from the other color depth
		c := &s.all[i]
		if (sameColor(bg, c.bgRGB) || sameColor(bg, ansi.IndexedColor(c.bgANSI))) &&
			(sameColor(fg, c.fgRGB) || sameColor(fg, ansi.IndexedColor(c.fgANSI))) {
			return i
		}
	}
	return -1
}

// nextCombo is the pair after i with the next background in its theme,
// and the same text color position (new_window_next_colors).
func (s *comboSet) nextCombo(i int) int {
	c := s.get(i)
	if c == nil {
		return -1
	}
	list := s.byTheme[c.theme]
	p := s.position(i)
	if p < 0 || len(list) == 0 {
		return -1
	}
	n := (p + 3) % len(list)
	return list[n]
}

// themeTone is whether a theme's backgrounds are dark or light (between:
// by the background itself).
func (s *comboSet) tone(c *combo) tone {
	switch comboThemes[c.theme] {
	case "dark":
		return toneDark
	case "light":
		return toneLight
	}
	return toneOf(c.bgRGB)
}

// paletteRGB reports whether palettes use their #RRGGBB colors:
// palette_colors "rgb" (the default) always, "ansi" never, "auto" when
// goat passes 24-bit color on.
func paletteRGB(setting string, trueColor bool) bool {
	switch setting {
	case "ansi":
		return false
	case "auto":
		return trueColor
	}
	return true
}
