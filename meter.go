package main

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

// Meters: a value drawn as a horizontal bar beside its number, in the
// status (F2) and inventory (F3) windows: CPU, memory, a program's
// progress.
//
//	██████████▋░░░░░░░░░░░░░░ 43%     (░: the track, a shade of the background)
//
// The bar is filled in eighths of a cell (barEighths, iconography.go), so
// a bar 25 cells wide shows the value to half a percent (200 steps). The
// part not filled is the track: the window's background made a little
// lighter (on a dark background) or darker (on a light one), so the
// bar's whole length shows without marks at its ends. Anything above zero
// shows at least an eighth.

// meterBar draws frac (0 to 1; outside that, the nearest end) as a bar of
// width cells. track is the escape sequence that sets the track's
// background (meterTrack); the bar ends with the window's background
// again.
func meterBar(frac float64, width int, track string) string {
	if width < 1 {
		return ""
	}
	if math.IsNaN(frac) || frac < 0 {
		frac = 0
	}
	frac = min(frac, 1)
	eighths := int(math.Round(frac * float64(width*8)))
	if eighths == 0 && frac > 0 {
		eighths = 1
	}
	full, part := eighths/8, eighths%8
	var b strings.Builder
	b.WriteString(strings.Repeat(string(barEighths[7]), full))
	if full == width {
		return b.String()
	}
	b.WriteString(track) // the rest of the bar: the partial cell's empty part too
	rest := width - full
	if part > 0 {
		b.WriteRune(barEighths[part-1])
		rest--
	}
	b.WriteString(strings.Repeat(" ", rest))
	b.WriteString("\x1b[49m") // the window's background
	return b.String()
}

// meterTrack is the escape sequence for a bar's track in w: its
// background (or the terminal's), a step lighter if it is dark, darker if
// it is light; one of the 256 colors, so it is the same everywhere.
func (m *WM) meterTrack(w *Window) string {
	var bg color.Color
	if w != nil {
		bg = w.bgColor
	}
	if bg == nil {
		bg = m.hostBg
	}
	if bg == nil {
		bg = ansi.IndexedColor(16) // unknown: take it for black, the usual
	}
	return fmt.Sprintf("\x1b[48;5;%dm", trackColor(bg))
}

// trackColor is the 256-color index of a track on bg: the nearest that
// is visibly apart from it.
func trackColor(bg color.Color) int {
	r, g, b := rgb(bg)
	to := 1.0 // toward white
	if luminance(bg) > 0.18 {
		to = 0 // toward black
	}
	for _, t := range []float64{0.15, 0.22, 0.3, 0.4, 0.5} {
		c := nearest256(mix(r, to, t), mix(g, to, t), mix(b, to, t))
		if contrastRatio(luminance(c), luminance(bg)) >= 1.25 {
			return int(c.(ansi.IndexedColor))
		}
	}
	return int(nearest256(mix(r, to, 0.5), mix(g, to, 0.5), mix(b, to, 0.5)).(ansi.IndexedColor))
}

// fitWide shortens a line with escape sequences in it (bars) to w
// columns, like fitStart does plain text.
func fitWide(s string, w int) string {
	if !strings.Contains(s, "\x1b") {
		return fitStart(s, w)
	}
	return ansi.Truncate(s, w, ellipsis)
}

// meterWidth is how wide a bar is with room cells for it:
// 25 (half a percent) when there is room, else what fits (at least 6, 48
// steps).
func meterWidth(room int) int {
	return clamp(room, 6, 25)
}

// share is part of whole (0 when the whole isn't known).
func share(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	return part / whole
}

// physicalMemory is the machine's memory in bytes (0 if unknown), read
// once (memoryTotal: procinfo_linux.go, procinfo_other.go).
var physicalMemory = sync.OnceValue(memoryTotal)
