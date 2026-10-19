package main

import "image/color"

// Whether a background is dark or light: the text and frame colors that
// suit it depend on it. Window colors come from palettes (combos.go).

type tone int

const (
	toneDark tone = iota
	toneLight
)

func (t tone) String() string { return [...]string{"dark", "light"}[t] }

// toneOf guesses whether c is a dark or light color (perceived lightness).
func toneOf(c color.Color) tone {
	r, g, b, _ := c.RGBA()
	y := 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b) // 0..65535
	if y > 0.5*65535 {
		return toneLight
	}
	return toneDark
}
