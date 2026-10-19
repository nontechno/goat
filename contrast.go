package main

import (
	"fmt"
	"image/color"
	"math"

	"github.com/charmbracelet/x/ansi"
)

// Border colors on colored windows.
//
// A window's frame takes its Alt+b background, so the theme's border colors
// (cyan, gray, yellow by default) can disappear on some backgrounds: gray on
// a dark gray, yellow on a light yellow. visibleOn keeps a color when it
// reads well enough on the background, and otherwise moves it lighter (on a
// dark background) or darker (on a light one), keeping its hue, until it
// does. The result is one of the 256 palette colors (16-255), so it shows
// the same on terminals without 24-bit color (macOS Terminal).

// Minimum contrast (WCAG ratio, 1..21) for each part of the frame. The
// unfocused border is allowed to stay fainter than the focused one.
const (
	contrastLabel     = 4.5 // title, host, wrap state, scroll marker
	contrastBorder    = 3.0 // focused or pinned border
	contrastUnfocused = 2.0 // unfocused border
)

type contrastKey struct {
	fg, bg color.Color
	min    float64
}

// visibleOn returns fg, or a lighter/darker shade of it, that has at least
// min contrast against bg. A nil bg (the terminal's own, unknown) or fg
// is returned as is.
func (m *WM) visibleOn(fg, bg color.Color, min float64) color.Color {
	if fg == nil || bg == nil {
		return fg
	}
	k := contrastKey{fg, bg, min}
	if c, ok := m.contrastCache[k]; ok {
		return c
	}
	c := adjustContrast(fg, bg, min)
	if m.contrastCache == nil {
		m.contrastCache = map[contrastKey]color.Color{}
	}
	if len(m.contrastCache) > 4096 { // only a few combinations are ever used
		clear(m.contrastCache)
	}
	m.contrastCache[k] = c
	return c
}

func adjustContrast(fg, bg color.Color, min float64) color.Color {
	lb := luminance(bg)
	if contrastRatio(luminance(fg), lb) >= min {
		return fg
	}
	// Toward white on a dark background, toward black on a light one; if
	// that can't reach min (a mid-gray background), the other way may.
	towards := []float64{1, 0}
	if lb > 0.18 { // about where white and black give the same contrast
		towards = []float64{0, 1}
	}
	r, g, b := rgb(fg)
	best, bestCR := fg, contrastRatio(luminance(fg), lb)
	for _, to := range towards {
		for step := 1; step <= 10; step++ {
			t := float64(step) / 10
			c := nearest256(mix(r, to, t), mix(g, to, t), mix(b, to, t))
			cr := contrastRatio(luminance(c), lb)
			if cr >= min {
				return c
			}
			if cr > bestCR {
				best, bestCR = c, cr
			}
		}
	}
	return best
}

func mix(v, to, t float64) float64 { return v + (to-v)*t }

// rgb is c's red, green and blue, 0..1.
func rgb(c color.Color) (r, g, b float64) {
	R, G, B, _ := c.RGBA()
	return float64(R) / 0xffff, float64(G) / 0xffff, float64(B) / 0xffff
}

// luminance is c's relative luminance (WCAG), 0 (black) .. 1 (white).
func luminance(c color.Color) float64 {
	lin := func(v float64) float64 {
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	r, g, b := rgb(c)
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

func contrastRatio(l1, l2 float64) float64 {
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// nearest256 is the palette color (16-255: the 6x6x6 cube and the grays,
// whose colors are fixed, unlike 0-15) closest to r, g, b (0..1).
func nearest256(r, g, b float64) color.Color {
	best, bestD := 16, math.Inf(1)
	for i := 16; i < 256; i++ {
		cr, cg, cb := rgb(ansi.IndexedColor(i))
		d := (cr-r)*(cr-r)*0.30 + (cg-g)*(cg-g)*0.59 + (cb-b)*(cb-b)*0.11
		if d < bestD {
			best, bestD = i, d
		}
	}
	return ansi.IndexedColor(best)
}

// ---- text colors -------------------------------------------------------------
//
// A window's text color must stay readable on its background: Alt+f skips
// text colors that aren't (theme.min_text_contrast), and Alt+b, new windows
// and the saved background-window colors change the text color when the
// background would hide it. "The terminal's own" color (nil) counts as the
// color the terminal reported, or a guess from its background.

// shownBg is the background w's text is drawn on.
func (m *WM) shownBg(bg color.Color) color.Color {
	switch {
	case bg != nil:
		return bg
	case m.hostBg != nil:
		return m.hostBg
	}
	return ansi.IndexedColor(16) // most terminals are dark
}

// shownFg is the text color drawn for fg.
func (m *WM) shownFg(fg color.Color) color.Color {
	switch {
	case fg != nil:
		return fg
	case m.hostFg != nil:
		return m.hostFg
	case m.hostBg != nil && toneOf(m.hostBg) == toneLight:
		return ansi.IndexedColor(235)
	}
	return ansi.IndexedColor(252)
}

// textContrast is the contrast of text color fg on background bg (nil:
// the terminal's own).
func (m *WM) textContrast(fg, bg color.Color) float64 {
	return contrastRatio(luminance(m.shownFg(fg)), luminance(m.shownBg(bg)))
}

// readable says whether text in fg shows well enough on bg. The
// terminal's own pair always counts as readable.
func (m *WM) readable(fg, bg color.Color) bool {
	min := m.cfg.Theme.MinTextContrast
	if min <= 0 || (fg == nil && bg == nil) {
		return true
	}
	return m.textContrast(fg, bg) >= min
}

// fixText makes w's text color readable on its background, if it isn't:
// the terminal's own if that reads, else whichever of black and white
// reads better. It reports whether it changed anything. (Palettes are
// readable as they are: this is for colors set otherwise, e.g. in a
// launch script.)
func (m *WM) fixText(w *Window) bool {
	if m.readable(w.fgColor, w.bgColor) {
		return false
	}
	w.pal = 0
	if m.readable(nil, w.bgColor) {
		w.fgColor = nil
		return true
	}
	w.fgColor = blackOrWhite(w.bgColor)
	return true
}

// textName names w's text color for messages.
func (m *WM) textName(w *Window) string {
	if w.pal > 0 {
		return m.comboName(w.pal-1) + "'s text color"
	}
	if w.fgColor == nil {
		return "the terminal's own"
	}
	return colorString(w.fgColor)
}

// colorString writes c as goat's config does: a palette number, "#rrggbb",
// or "default".
func colorString(c color.Color) string {
	switch c := c.(type) {
	case nil:
		return "default"
	case ansi.IndexedColor:
		return fmt.Sprint(uint8(c))
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// sameColor says whether a and b are the same color (nil: the terminal's).
func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// looksSame says whether a and b would look alike on screen: each of red,
// green and blue within 6 (of 255). Neighbouring colors of the 256-color
// palette (the grays are 10 apart) still count as different.
func looksSame(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	d := func(x, y uint32) bool {
		x, y = x>>8, y>>8
		return x-y <= 6 || y-x <= 6
	}
	return d(ar, br) && d(ag, bg) && d(ab, bb)
}
