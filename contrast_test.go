package main

import (
	"image/color"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestVisibleOnKeepsReadableColors(t *testing.T) {
	m := &WM{}
	cyan := ansi.IndexedColor(14)
	if got := m.visibleOn(cyan, ansi.IndexedColor(17), contrastBorder); got != cyan { // cyan on navy
		t.Errorf("cyan on navy changed to %v", got)
	}
	if got := m.visibleOn(cyan, nil, contrastLabel); got != cyan {
		t.Errorf("no background: changed to %v", got)
	}
}

func TestVisibleOnFixesInvisibleColors(t *testing.T) {
	m := &WM{}
	for _, tc := range []struct {
		name   string
		fg, bg color.Color
		min    float64
		darker bool
	}{
		{"gray on dark gray", ansi.IndexedColor(8), ansi.IndexedColor(237), contrastUnfocused, false},
		{"cyan on pale cyan", ansi.IndexedColor(14), ansi.IndexedColor(195), contrastBorder, true},
		{"yellow on cream", ansi.IndexedColor(11), ansi.IndexedColor(230), contrastLabel, true},
	} {
		got := m.visibleOn(tc.fg, tc.bg, tc.min)
		cr := contrastRatio(luminance(got), luminance(tc.bg))
		if cr < tc.min {
			t.Errorf("%s: %v has contrast %.2f < %.1f", tc.name, got, cr, tc.min)
		}
		if d := luminance(got) < luminance(tc.fg); d != tc.darker {
			t.Errorf("%s: darker=%v, want %v", tc.name, d, tc.darker)
		}
		if _, ok := got.(ansi.IndexedColor); !ok {
			t.Errorf("%s: %T, want a palette color", tc.name, got)
		}
	}
}

// Every palette's background gets readable borders and labels from the
// default theme, with the unfocused border fainter.
func TestBorderColorsOnPaletteBackgrounds(t *testing.T) {
	s := testCombos(t)
	th := DefaultConfig().Theme
	m := &WM{}
	for i := range s.all {
		c := &s.all[i]
		bg := struct {
			name, tone string
			c          color.Color
		}{c.names[0], comboThemes[c.theme], s.bg(c)}
		lb := luminance(bg.c)
		for _, c := range []struct {
			role string
			fg   color.Color
			min  float64
		}{
			{"focused", th.FocusedBorder.C, contrastBorder},
			{"pinned", th.PinnedBorder.C, contrastBorder},
			{"unfocused", th.UnfocusedBorder.C, contrastUnfocused},
			{"label", th.FocusedBorder.C, contrastLabel},
			{"pinned label", th.PinnedBorder.C, contrastLabel},
		} {
			got := m.visibleOn(c.fg, bg.c, c.min)
			if cr := contrastRatio(luminance(got), lb); cr < c.min {
				t.Errorf("%s (%s) %s: contrast %.2f < %.1f", bg.name, bg.tone, c.role, cr, c.min)
			}
		}
		f := contrastRatio(luminance(m.visibleOn(th.FocusedBorder.C, bg.c, contrastBorder)), lb)
		u := contrastRatio(luminance(m.visibleOn(th.UnfocusedBorder.C, bg.c, contrastUnfocused)), lb)
		if u > f+0.01 {
			t.Errorf("%s: unfocused border (%.2f) stands out more than focused (%.2f)", bg.name, u, f)
		}
	}
}

// The frame of a window with a light background gets darker border and
// title colors; one without keeps the theme's.
func TestDrawFrameOnLightBackground(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 30, 8)
	w := fakeFramed(1, 0, 0, 20, 6, true, frameCompact)
	m.windows, m.focused = []*Window{w}, w
	cellFg := func(x, y int) color.Color {
		scr := uv.NewScreen(m.cols, m.rows)
		scr.Clear()
		scene{m}.Draw(scr, scr.Bounds())
		return scr.CellAt(x, y).Style.Fg
	}
	cyan := m.cfg.Theme.FocusedBorder.C
	if got := cellFg(0, 2); got != cyan {
		t.Fatalf("default background: border %v, want %v", got, cyan)
	}
	w.bgColor = ansi.IndexedColor(231)           // white
	for _, p := range [][2]int{{0, 2}, {3, 0}} { // left side, title
		got := cellFg(p[0], p[1])
		if got == cyan || contrastRatio(luminance(got), luminance(w.bgColor)) < contrastBorder {
			t.Errorf("white background, cell %v: fg %v not adjusted", p, got)
		}
	}
}
