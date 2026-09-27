package main

import (
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.AltTimeoutMs != 200 || c.Layout.MinWindowRows != 5 || c.frame != frameFull {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if c.bindings['c'] != actNewWindow || c.bindings['H'] != actResizeLeft {
		t.Fatalf("default bindings wrong: %v", c.bindings)
	}
}

func TestParseConfigValues(t *testing.T) {
	c, warns := parseConfig(`
shell = "/bin/zsh"
compact = true
alt_timeout_ms = 50
[theme]
focused_border = 12
compact_title_bg = "#1e1e2e"
[keys]
new_window = "t"
`)
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	if c.Shell != "/bin/zsh" || c.frame != frameCompact || c.AltTimeoutMs != 50 {
		t.Fatalf("values not applied: %+v", c)
	}
	if c.Theme.FocusedBorder.C != ansi.IndexedColor(12) {
		t.Fatalf("focused_border = %v", c.Theme.FocusedBorder.C)
	}
	if got := c.Theme.CompactTitleBg.C; got != (color.RGBA{0x1e, 0x1e, 0x2e, 0xff}) {
		t.Fatalf("compact_title_bg = %v", got)
	}
	if c.bindings['t'] != actNewWindow || c.bindings['c'] != actNone {
		t.Fatalf("rebinding failed: %v", c.bindings)
	}
	// Untouched values keep their defaults.
	if c.Theme.UnfocusedBorder.C != ansi.IndexedColor(8) || c.Keys.Quit != "q" {
		t.Fatal("defaults lost for unset keys")
	}
}

func TestFloatLayoutCompat(t *testing.T) {
	// float's example config put alt_timeout_ms under [layout].
	c, _ := parseConfig("[layout]\nalt_timeout_ms = 0\n")
	if c.AltTimeoutMs != 0 {
		t.Fatalf("alt_timeout_ms under [layout] ignored: %d", c.AltTimeoutMs)
	}
	// Top level wins when both are set.
	c, _ = parseConfig("alt_timeout_ms = 120\n[layout]\nalt_timeout_ms = 0\n")
	if c.AltTimeoutMs != 120 {
		t.Fatalf("top-level alt_timeout_ms should win: %d", c.AltTimeoutMs)
	}
}

func TestConfigWarnings(t *testing.T) {
	c, warns := parseConfig(`
bogus = 1
title_source = "nope"
[keys]
quit = "qq"
focus_next = "c"
[layout]
new_window_width_ratio = 2.0
min_window_rows = 1
`)
	joined := strings.Join(warns, "\n")
	for _, want := range []string{`"bogus"`, "title_source", "keys.quit", "keys.focus_next", "new_window_width_ratio"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning about %s in:\n%s", want, joined)
		}
	}
	if c.Keys.Quit != "q" || c.TitleSource != "process" || c.Layout.NewWindowWidthRatio != 0.5 {
		t.Errorf("bad values not reset: %+v", c)
	}
	if c.Layout.MinWindowRows != 3 {
		t.Errorf("min_window_rows not clamped: %d", c.Layout.MinWindowRows)
	}
}

func TestParseErrorFallsBack(t *testing.T) {
	c, warns := parseConfig("compact = \n")
	if len(warns) != 1 || !strings.Contains(warns[0], "parse error") || c.frame != frameFull {
		t.Fatalf("got %v %+v", warns, c)
	}
}

func TestBadColor(t *testing.T) {
	for _, in := range []string{"focused_border = 300", `focused_border = "red"`, `focused_border = "#12345"`} {
		_, warns := parseConfig("[theme]\n" + in + "\n")
		if len(warns) == 0 {
			t.Errorf("%s: expected a warning", in)
		}
	}
}
