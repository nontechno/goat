package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// What a few terminals send for the learning keys, and what goat should
// make of it.
func TestAnalyzeLearn(t *testing.T) {
	steps := learnSteps(DefaultConfig())
	arrows := map[string]string{ // xterm-style Alt+arrows
		"alt+left": "\x1b[1;3D", "alt+right": "\x1b[1;3C", "alt+up": "\x1b[1;3A", "alt+down": "\x1b[1;3B",
		"alt+shift+left": "\x1b[1;4D",
	}
	with := func(base map[string]string, extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	for _, tc := range []struct {
		name      string
		sent      map[string]string // want -> bytes
		mac       string
		remap     map[string]string
		conflicts int
	}{
		{"xterm / iTerm2 (Esc+)", with(arrows, map[string]string{"alt+c": "\x1bc", "alt+b": "\x1bb", "alt+f": "\x1bf"}),
			"off", nil, 0},
		{"macOS Terminal, Option types characters", with(arrows, map[string]string{
			"alt+c": "ç", "alt+b": "∫", "alt+f": "ƒ", "alt+left": "\x1bb", "alt+right": "\x1bf"}),
			"on", map[string]string{"alt+b": "alt+left", "alt+f": "alt+right"}, 0},
		{"macOS Terminal, Option as Meta", with(arrows, map[string]string{
			"alt+c": "\x1bc", "alt+b": "\x1bb", "alt+f": "\x1bf", "alt+left": "\x1bb", "alt+right": "\x1bf"}),
			"off", nil, 2},
		{"Esc-prefixed arrows", with(arrows, map[string]string{
			"alt+c": "\x1bc", "alt+b": "\x1bb", "alt+f": "\x1bf", "alt+up": "\x1b\x1b[A"}),
			"off", nil, 0},
	} {
		got := map[string]string{}
		for want, b := range tc.sent {
			got[want] = decodeKey([]byte(b))
		}
		r := analyzeLearn(steps, got)
		if r.profile.MacOptionKeys != tc.mac {
			t.Errorf("%s: mac_option_keys %q, want %q", tc.name, r.profile.MacOptionKeys, tc.mac)
		}
		if len(r.profile.Remap) != len(tc.remap) {
			t.Errorf("%s: remap %v, want %v", tc.name, r.profile.Remap, tc.remap)
		}
		for k, v := range tc.remap {
			if r.profile.Remap[k] != v {
				t.Errorf("%s: remap[%s] = %q, want %q", tc.name, k, r.profile.Remap[k], v)
			}
		}
		if len(r.conflicts) != tc.conflicts {
			t.Errorf("%s: conflicts %v", tc.name, r.conflicts)
		}
	}
}

// Option as Meta: Option+Left and Option+b both send Esc b; the user's
// choice decides what it does.
func TestLearnConflictChoice(t *testing.T) {
	steps := learnSteps(DefaultConfig())
	got := map[string]string{"alt+c": "alt+c", "alt+b": "alt+b", "alt+f": "alt+f", "alt+left": "alt+b", "alt+right": "alt+f"}
	r := analyzeLearn(steps, got)
	if len(r.conflicts) != 2 || r.conflicts[0] != [2]string{"alt+b", "alt+left"} {
		t.Fatalf("conflicts %v", r.conflicts)
	}
	r.resolveConflict("alt+b", "alt+left") // move windows
	r.resolveConflict("alt+f", "alt+f")    // keep word right for programs
	if r.profile.Remap["alt+b"] != "alt+left" || len(r.profile.Remap) != 1 {
		t.Fatalf("remap %v", r.profile.Remap)
	}
}

// A key that arrives as the plain key can't be used: a note, no remap.
func TestLearnPlainArrow(t *testing.T) {
	steps := learnSteps(DefaultConfig())
	got := map[string]string{"alt+c": "alt+c", "alt+up": "up"}
	r := analyzeLearn(steps, got)
	if len(r.profile.Remap) != 0 || !strings.Contains(strings.Join(r.notes, "\n"), `plain "up"`) {
		t.Fatalf("remap %v notes %v", r.profile.Remap, r.notes)
	}
}

func TestTermProfileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.toml")
	if err := os.WriteFile(path, []byte(userLocal), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = writeDesktopColors(path, desktopColors{bg: ansi.IndexedColor(17)}, [2]string{})
	emoji := false
	p := termProfile{MacOptionKeys: "on", Emoji: &emoji, Remap: map[string]string{"alt+b": "alt+left", "alt+f": "alt+right"}}
	if err := writeTermProfile(path, "Apple_Terminal", p); err != nil {
		t.Fatal(err)
	}
	p2 := termProfile{MacOptionKeys: "off"}
	if err := writeTermProfile(path, "iTerm.app", p2); err != nil {
		t.Fatal(err)
	}
	p.Remap["alt+f"] = "alt+f" // learned again: replaced, not added
	delete(p.Remap, "alt+f")
	if err := writeTermProfile(path, "Apple_Terminal", p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if !strings.HasPrefix(text, userLocal) || strings.Count(text, `[terminal."Apple_Terminal"]`) != 1 ||
		!strings.Contains(text, "[background_window]") {
		t.Fatalf("local.toml:\n%s", text)
	}
	got, ok, w := readTermProfile(path, "Apple_Terminal")
	if !ok || w != "" || got.MacOptionKeys != "on" || got.Emoji == nil || *got.Emoji ||
		len(got.Remap) != 1 || got.Remap["alt+b"] != "alt+left" {
		t.Fatalf("read back %+v %v %q\n%s", got, ok, w, text)
	}
	if got, ok, _ := readTermProfile(path, "iTerm.app"); !ok || got.MacOptionKeys != "off" {
		t.Fatalf("iTerm.app: %+v %v", got, ok)
	}
	if _, ok, _ := readTermProfile(path, "unknown"); ok {
		t.Fatal("a profile for an unknown terminal")
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `hostname = "friendly"`) {
		t.Fatal("other lines of local.toml lost")
	}
}

func TestKeyFromKeystroke(t *testing.T) {
	for s, want := range map[string]uv.Key{
		"alt+left":       {Code: uv.KeyLeft, Mod: uv.ModAlt},
		"alt+shift+left": {Code: uv.KeyLeft, Mod: uv.ModAlt | uv.ModShift},
		"alt+b":          {Code: 'b', Mod: uv.ModAlt},
	} {
		if k, ok := keyFromKeystroke(s); !ok || k.Code != want.Code || k.Mod != want.Mod || k.Keystroke() != s {
			t.Errorf("%s: %+v %v", s, k, ok)
		}
	}
	if _, ok := keyFromKeystroke("hyper+x"); ok {
		t.Error("hyper+x accepted")
	}
}

// With a learned profile, goat reads keys as it says, and the "auto"
// guesses step aside.
func TestTermProfileInUse(t *testing.T) {
	m, _, _, w := wmWithBackground(t)
	paletteOnB(t, m)
	m.optionArrows, m.arrowsLearn = true, true // as in macOS Terminal
	emoji := false
	m.applyTermProfile(termProfile{MacOptionKeys: "off", Emoji: &emoji, Remap: map[string]string{"alt+f": "alt+right"}})
	if m.optionArrows || m.arrowsLearn || m.macOption {
		t.Fatalf("guesses still on: %v %v %v", m.optionArrows, m.arrowsLearn, m.macOption)
	}
	x := w.x
	_ = m.handleKey(uv.Key{Code: 'b', Mod: uv.ModAlt}) // not remapped: a shortcut (palettes)
	if w.x != x || m.palDlg == nil {
		t.Fatalf("Alt+b: x %d -> %d, palettes %v", x, w.x, m.palDlg != nil)
	}
	m.closePalette(false)
	_ = m.handleKey(uv.Key{Code: 'f', Mod: uv.ModAlt}) // remapped: move right
	if w.x != x+1 {
		t.Fatalf("Alt+f (learned Alt+Right): x %d -> %d", x, w.x)
	}
	m.host, m.user = "box", "alice"
	if got := m.frameLabel(w); got != "alice@box" {
		t.Fatalf("no emoji: frame label %q", got)
	}
	// config.toml's explicit settings win over the profile.
	m2, _, _, _ := wmWithBackground(t)
	m2.cfg.MacOptionKeys = "on"
	m2.macOption = true
	m2.applyTermProfile(termProfile{MacOptionKeys: "off"})
	if !m2.macOption {
		t.Fatal("profile overrode mac_option_keys = on")
	}
}

func TestParseCPR(t *testing.T) {
	if col, ok := parseCPR([]byte("\x1b[12;5R")); !ok || col != 5 {
		t.Fatalf("%d %v", col, ok)
	}
	if _, ok := parseCPR([]byte("x")); ok {
		t.Fatal("parsed garbage")
	}
}
