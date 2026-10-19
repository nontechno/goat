package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMeterBar(t *testing.T) {
	const tr = "\x1b[48;5;236m" // a track
	const end = "\x1b[49m"
	for _, tc := range []struct {
		frac  float64
		width int
		want  string
	}{
		{0, 4, tr + "    " + end},
		{1, 4, "████"}, // full: no track
		{0.5, 4, "██" + tr + "  " + end},
		{1.0 / 32, 4, tr + "▏   " + end}, // one eighth of a cell, its empty part on the track
		{7.0 / 32, 4, tr + "▉   " + end}, // seven eighths
		{9.0 / 32, 4, "█" + tr + "▏  " + end},
		{0.001, 4, tr + "▏   " + end}, // anything above zero shows
		{2, 4, "████"},                // beyond the end: full
		{-1, 4, tr + "    " + end},
	} {
		if got := meterBar(tc.frac, tc.width, tr); got != tc.want {
			t.Errorf("%.4f in %d: %q, want %q", tc.frac, tc.width, got, tc.want)
		}
	}
	// 25 cells: half a percent apart shows; always 25 cells, no end marks.
	if meterBar(0.500, 25, tr) == meterBar(0.505, 25, tr) {
		t.Error("half a percent apart look the same")
	}
	for _, f := range []float64{0, 0.33, 0.505, 1} {
		if n := ansi.StringWidth(meterBar(f, 25, tr)); n != 25 {
			t.Errorf("%.3f: %d cells", f, n)
		}
	}
	if !strings.HasPrefix(meterBar(0.5, 25, tr), "████████████"+tr+"▌") {
		t.Errorf("half of 25: %q", meterBar(0.5, 25, tr))
	}
	if meterBar(0.5, 0, tr) != "" || meterWidth(100) != 25 || meterWidth(3) != 6 || meterWidth(20) != 20 {
		t.Error("widths")
	}
}

// The track is a shade off the background: lighter on dark ones, darker
// on light ones, never the same color.
func TestMeterTrack(t *testing.T) {
	for _, bg := range []int{16, 17, 233, 236, 24, 52, 94, 145, 187, 230, 231, 255} {
		c := ansi.IndexedColor(bg)
		tc := ansi.IndexedColor(trackColor(c))
		if trackColor(c) == bg {
			t.Errorf("%d: the track is the background", bg)
		}
		lighter := luminance(tc) > luminance(c)
		if dark := luminance(c) <= 0.18; dark != lighter {
			t.Errorf("%d → %d: lighter %v", bg, trackColor(c), lighter)
		}
	}
	m := &WM{}
	w := &Window{bgColor: ansi.IndexedColor(231)}
	if got := m.meterTrack(w); got == m.meterTrack(nil) || !strings.HasPrefix(got, "\x1b[48;5;") {
		t.Errorf("track %q", got)
	}
}

func TestPhysicalMemory(t *testing.T) {
	if physicalMemory() == 0 {
		t.Skip("total memory not known here")
	}
	if physicalMemory() < 64<<20 {
		t.Errorf("%d bytes", physicalMemory())
	}
}
