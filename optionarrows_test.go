package main

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestMacOptionArrowsOn(t *testing.T) {
	for _, tc := range []struct {
		setting, termProgram string
		on, learn            bool
	}{
		{"auto", "Apple_Terminal", true, true}, // macOS Terminal: on, still watching
		{"auto", "", false, false},             // unknown (ssh, Linux, Windows): Alt+b is Alt+b
		{"auto", "iTerm.app", false, false},
		{"auto", "WezTerm", false, false},
		{"on", "iTerm.app", true, false},
		{"off", "Apple_Terminal", false, false},
	} {
		on, learn := macOptionArrowsOn(tc.setting, tc.termProgram)
		if on != tc.on || learn != tc.learn {
			t.Errorf("%+v: on %v learn %v", tc, on, learn)
		}
	}
}

// macOS Terminal with "Use Option as Meta key": Option+c arrives as Esc c,
// which shows Esc b is Option+b, not Option+Left: Alt+b changes colors.
func TestOptionArrowsLearnMeta(t *testing.T) {
	m, _, _, w := wmWithBackground(t)
	m.palette = fullPalette(t)
	m.optionArrows, m.arrowsLearn = true, true
	_ = m.handleKey(uv.Key{Code: 'n', Mod: uv.ModAlt}) // Esc n: Option as Meta
	if m.optionArrows || m.arrowsLearn {
		t.Fatalf("still taking Esc b as an arrow: %v %v", m.optionArrows, m.arrowsLearn)
	}
	w = m.focused
	x := w.x
	_ = m.handleKey(uv.Key{Code: 'b', Mod: uv.ModAlt})
	if w.x != x || w.bgColor == nil {
		t.Fatalf("Alt+b moved the window (x %d -> %d) or kept the colors (%v)", x, w.x, w.bgColor)
	}
}

// Without Meta, Option+letters type characters: one of those settles it the
// other way, and Esc b stays Option+Left even after other keys.
func TestOptionArrowsLearnCharacters(t *testing.T) {
	m, _, _, w := wmWithBackground(t)
	m.palette = fullPalette(t)
	m.optionArrows, m.arrowsLearn, m.macOption = true, true, true
	_ = m.handleKey(uv.Key{Code: '∫', Text: "∫"}) // Option+b without Meta
	if !m.optionArrows || m.arrowsLearn {
		t.Fatalf("after ∫: on %v learn %v", m.optionArrows, m.arrowsLearn)
	}
	_ = m.handleKey(uv.Key{Code: 'y', Mod: uv.ModAlt}) // decided: no change (Alt+y isn't bound)
	x := w.x
	_ = m.handleKey(uv.Key{Code: 'b', Mod: uv.ModAlt})
	if w.x != x-1 {
		t.Fatalf("Option+Left didn't move the window: %d -> %d", x, w.x)
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
