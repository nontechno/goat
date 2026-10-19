package main

import (
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

// The settings for colors that now come from palettes (and the old
// compact = true/false) are reported as unknown, not silently used.
func TestRemovedSettings(t *testing.T) {
	for _, in := range []string{
		"[theme]\nwindow_colors = [\"17:230\"]\n",
		"[theme]\nwindow_backgrounds = [17]\n",
		"compact = true\n",
	} {
		c, warns := parseConfig(in)
		if len(warns) != 1 || !strings.Contains(warns[0], "unknown setting") || c.frame != frameFull {
			t.Errorf("%q: %v", in, warns)
		}
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
