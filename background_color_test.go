package main

import (
	"image/color"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

var (
	c17  = ansi.IndexedColor(17)
	c22  = ansi.IndexedColor(22)
	c230 = ansi.IndexedColor(230)
)

func TestWindowColorsConfig(t *testing.T) {
	c, warns := parseConfig(`
[theme]
window_colors = ["default:default", "17:230", "#102030:#e0e0e0", 22, {bg = 52, fg = 224}, "53"]
[keys]
cycle_background = "g"
`)
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	got := c.Theme.WindowColors
	want := []ColorPair{
		{nil, nil},
		{c17, c230},
		{color.RGBA{0x10, 0x20, 0x30, 0xff}, color.RGBA{0xe0, 0xe0, 0xe0, 0xff}},
		{c22, nil},
		{ansi.IndexedColor(52), ansi.IndexedColor(224)},
		{ansi.IndexedColor(53), nil},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries: %+v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: %+v want %+v", i, got[i], want[i])
		}
	}
	if c.bindings['g'] != actCycleBackground {
		t.Fatal("cycle_background binding")
	}
	for _, bad := range []string{`["blue:red"]`, `["17:300"]`, `[{bg = 17, color = 3}]`} {
		if _, warns := parseConfig("[theme]\nwindow_colors = " + bad + "\n"); len(warns) == 0 {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestWindowBackgroundsStillWork(t *testing.T) {
	c, warns := parseConfig("[theme]\nwindow_backgrounds = [\"default\", 17]\n")
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	if got := c.Theme.WindowColors; len(got) != 2 || got[1] != (ColorPair{c17, nil}) {
		t.Fatalf("converted to %+v", got)
	}
}

func TestCycleColors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Theme.WindowColors = []ColorPair{{}, {c17, c230}, {c22, nil}}
	m := newWM(cfg, nil, 30, 8)
	w := fakeWindow(1, 0, 0, 12, 5, false, false)
	m.initBackground(w)
	m.windows, m.focused = []*Window{w}, w
	if w.colorIndex != 0 || w.bgColor != nil || w.fgColor != nil {
		t.Fatalf("initial %d %v %v", w.colorIndex, w.bgColor, w.fgColor)
	}
	var steps []ColorPair
	for range 4 {
		_, _ = m.shortcut(uv.Key{Code: 'b', Mod: uv.ModAlt})
		steps = append(steps, ColorPair{w.bgColor, w.fgColor})
	}
	want := []ColorPair{{c17, c230}, {c22, nil}, {}, {c17, c230}}
	for i := range want {
		if steps[i] != want[i] {
			t.Fatalf("step %d: %+v want %+v", i, steps[i], want[i])
		}
	}
	if !strings.Contains(m.status, "colors 1/3") && !strings.Contains(m.status, "colors 2/3: 17:230") {
		t.Fatalf("status %q", m.status)
	}
	// Without a default entry the first press picks the first scheme.
	cfg.Theme.WindowColors = []ColorPair{{c22, c230}}
	m.initBackground(w)
	_ = m.run(actCycleBackground)
	if w.bgColor != c22 || w.fgColor != c230 {
		t.Fatalf("first press: %v %v", w.bgColor, w.fgColor)
	}
}

func TestWindowColorsRendering(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 20, 6)
	w := fakeWindow(1, 0, 0, 12, 4, false, false)
	_, _ = w.emu.WriteString("ab\x1b[41;33mR\x1b[0m")
	w.bgColor, w.fgColor = c17, c230
	m.windows, m.focused = []*Window{w}, w
	scr := uv.NewScreen(20, 6)
	scene{m}.Draw(scr, scr.Bounds())

	if c := scr.CellAt(1, 1); c.Content != "a" || c.Style.Bg != c17 || c.Style.Fg != c230 {
		t.Fatalf("plain text %+v", c.Style)
	}
	if c := scr.CellAt(3, 1); c.Style.Bg != ansi.BasicColor(1) || c.Style.Fg != ansi.BasicColor(3) {
		t.Fatalf("program's own colors must win: %+v", c.Style)
	}
	if c := scr.CellAt(8, 2); c.Style.Bg != c17 {
		t.Fatalf("blank cell %+v", c.Style)
	}
	// Border cells take the window background; their line color stays.
	for _, p := range [][2]int{{0, 0}, {5, 0}, {0, 1}, {11, 2}, {4, 3}} {
		c := scr.CellAt(p[0], p[1])
		if c.Style.Bg != c17 || c.Style.Fg != m.cfg.Theme.FocusedBorder.C {
			t.Fatalf("border %v: %+v", p, c.Style)
		}
	}
}

func TestWindowColorBeatsCompactTitleBg(t *testing.T) {
	cfg := DefaultConfig()
	cfg.frame = frameCompact
	tb := idx(236)
	cfg.Theme.CompactTitleBg = &tb
	m := newWM(cfg, nil, 20, 6)
	w := fakeWindow(1, 0, 0, 12, 4, true, true)
	m.windows, m.focused = []*Window{w}, w
	scr := uv.NewScreen(20, 6)
	scene{m}.Draw(scr, scr.Bounds())
	if c := scr.CellAt(0, 0); c.Style.Bg != tb.C {
		t.Fatalf("no window color: want compact_title_bg, got %v", c.Style.Bg)
	}
	w.bgColor = c22
	scr = uv.NewScreen(20, 6)
	scene{m}.Draw(scr, scr.Bounds())
	if c := scr.CellAt(0, 0); c.Style.Bg != c22 {
		t.Fatalf("window color should win, got %v", c.Style.Bg)
	}
}
