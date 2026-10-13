package main

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestMacOptionArrowsOn(t *testing.T) {
	for _, tc := range []struct {
		setting           string
		macOn             bool
		termProgram, lcTm string
		want              bool
	}{
		{"auto", false, "Apple_Terminal", "", true}, // macOS Terminal, on the Mac
		{"auto", true, "", "", true},                // over ssh, mac_option_keys on, terminal unknown
		{"auto", true, "", "iTerm2", false},         // iTerm2 over ssh (LC_TERMINAL)
		{"auto", true, "iTerm.app", "iTerm2", false},
		{"auto", false, "", "", false}, // not a Mac
		{"auto", false, "WezTerm", "", false},
		{"on", false, "iTerm.app", "iTerm2", true},
		{"off", true, "Apple_Terminal", "", false},
	} {
		if got := macOptionArrowsOn(tc.setting, tc.macOn, tc.termProgram, tc.lcTm); got != tc.want {
			t.Errorf("%+v: %v", tc, got)
		}
	}
}

// With mac_option_arrows: the Alt+b / Alt+f macOS Terminal sends for
// Option+Left / Option+Right move the window; Option+b (∫, with
// mac_option_keys) and Esc then b still change the background.
func TestOptionArrowsMoveWindow(t *testing.T) {
	m, _, _, w := wmWithBackground(t)
	m.palette = fullPalette(t)
	m.optionArrows, m.macOption = true, true
	m.cfg.AltTimeoutMs = 200
	x := w.x

	_ = m.handleKey(uv.Key{Code: 'b', Mod: uv.ModAlt}) // Esc b from the terminal
	if w.x != x-1 || w.bgColor != nil {
		t.Fatalf("Option+Left: x %d -> %d, bg %v", x, w.x, w.bgColor)
	}
	_ = m.handleKey(uv.Key{Code: 'f', Mod: uv.ModAlt}) // Esc f
	if w.x != x || w.fgColor != nil {
		t.Fatalf("Option+Right: x %d, fg %v", w.x, w.fgColor)
	}

	_ = m.handleKey(uv.Key{Code: '∫', Text: "∫"}) // Option+b without Meta
	if w.bgColor == nil {
		t.Fatal("Option+b (∫) didn't change the background")
	}
	bg := w.bgColor
	_ = m.handleKey(uv.Key{Code: uv.KeyEscape}) // Esc, then b: Alt+b
	_ = m.handleKey(uv.Key{Code: 'b', Text: "b"})
	if w.bgColor == bg || w.x != x {
		t.Fatalf("Esc then b: bg %v (was %v), x %d", w.bgColor, bg, w.x)
	}
}

func TestOptionArrowsOff(t *testing.T) {
	m, _, _, w := wmWithBackground(t)
	m.palette = fullPalette(t)
	x := w.x
	_ = m.handleKey(uv.Key{Code: 'b', Mod: uv.ModAlt})
	if w.x != x || w.bgColor == nil {
		t.Fatalf("Alt+b: x %d -> %d, bg %v", x, w.x, w.bgColor)
	}
}

func TestKeyTestDescribesOptionArrows(t *testing.T) {
	cfg := DefaultConfig()
	if got := describeKeyBytes(cfg, []byte("\x1bb"), true, true); !strings.Contains(got, "move left") {
		t.Fatalf("on: %q", got)
	}
	if got := describeKeyBytes(cfg, []byte("\x1bb"), true, false); !strings.Contains(got, "background") {
		t.Fatalf("off: %q", got)
	}
	if got := describeKeyBytes(cfg, []byte("\x1b[1;3D"), false, false); !strings.Contains(got, "move left") {
		t.Fatalf("CSI: %q", got)
	}
}
