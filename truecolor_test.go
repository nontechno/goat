package main

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestHostTrueColor(t *testing.T) {
	var out bytes.Buffer // not a terminal: only the environment counts
	for _, tc := range []struct {
		name    string
		setting string
		env     []string
		want    bool
	}{
		{"ssh, no COLORTERM", "auto", []string{"TERM=xterm-256color"}, false},
		{"COLORTERM", "auto", []string{"TERM=xterm-256color", "COLORTERM=truecolor"}, true},
		{"24bit", "auto", []string{"TERM=xterm-256color", "COLORTERM=24bit"}, true},
		{"iTerm2 locally", "auto", []string{"TERM=xterm-256color", "TERM_PROGRAM=iTerm.app"}, true},
		{"iTerm2 over ssh", "auto", []string{"TERM=xterm-256color", "LC_TERMINAL=iTerm2"}, true},
		{"macOS Terminal", "auto", []string{"TERM=xterm-256color", "TERM_PROGRAM=Apple_Terminal"}, false},
		{"direct", "auto", []string{"TERM=xterm-direct"}, true},
		{"tmux ignores COLORTERM", "auto", []string{"TERM=tmux-256color", "COLORTERM=truecolor"}, false},
		{"on", "on", []string{"TERM=xterm-256color"}, true},
		{"off", "off", []string{"TERM=xterm-256color", "COLORTERM=truecolor"}, false},
	} {
		got, why := hostTrueColor(tc.setting, &out, tc.env)
		if got != tc.want || why == "" {
			t.Errorf("%s: %v (%s), want %v", tc.name, got, why, tc.want)
		}
	}
	if p := colorProfile(false); p != colorprofile.ANSI256 {
		t.Errorf("off: profile %v", p)
	}
	if p := colorProfile(true); p != colorprofile.TrueColor {
		t.Errorf("on: profile %v", p)
	}
}

// Programs are told COLORTERM=truecolor only when goat passes it on.
func TestChildEnvColorterm(t *testing.T) {
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "screen")
	has := func(env []string, kv string) bool { return slices.Contains(env, kv) }
	on, off := childEnv(true), childEnv(false)
	if !has(on, "COLORTERM=truecolor") || !has(on, "TERM=xterm-256color") || has(on, "TERM=screen") {
		t.Errorf("on: %v", on)
	}
	for _, kv := range off {
		if strings.HasPrefix(kv, "COLORTERM=") {
			t.Errorf("off: %s", kv)
		}
	}
	if !has(off, "TERM=xterm-256color") {
		t.Error("off: no TERM")
	}
}

// Programs are told they run in goat, not in the terminal goat runs in.
func TestChildEnvHostTerminal(t *testing.T) {
	for _, kv := range []string{"TERM_PROGRAM=iTerm.app", "TERM_PROGRAM_VERSION=3.5", "LC_TERMINAL=iTerm2",
		"ITERM_SESSION_ID=w0t0p0", "KITTY_WINDOW_ID=3", "WT_SESSION=abc", "TMUX=/tmp/tmux,1,0",
		"WEZTERM_PANE=2", "GOAT_KEEP=yes"} {
		name, val, _ := strings.Cut(kv, "=")
		t.Setenv(name, val)
	}
	env := childEnv(false)
	got := map[string]string{}
	for _, kv := range env {
		name, val, _ := strings.Cut(kv, "=")
		if _, dup := got[name]; dup {
			t.Errorf("%s twice", name)
		}
		got[name] = val
	}
	if got["TERM_PROGRAM"] != "goat" || got["TERM_PROGRAM_VERSION"] != version {
		t.Errorf("TERM_PROGRAM %q %q", got["TERM_PROGRAM"], got["TERM_PROGRAM_VERSION"])
	}
	for _, name := range []string{"LC_TERMINAL", "ITERM_SESSION_ID", "KITTY_WINDOW_ID", "WT_SESSION", "TMUX", "WEZTERM_PANE"} {
		if v, ok := got[name]; ok {
			t.Errorf("%s=%s passed on", name, v)
		}
	}
	if got["GOAT_KEEP"] != "yes" {
		t.Error("other variables are dropped")
	}
}

func TestTrueColorSetting(t *testing.T) {
	if c, w := parseConfig("true_color = \"on\"\n"); len(w) != 0 || c.TrueColor != "on" {
		t.Errorf("on: %v %v", c.TrueColor, w)
	}
	if c, w := parseConfig("true_color = \"maybe\"\n"); len(w) != 1 || c.TrueColor != "auto" {
		t.Errorf("maybe: %v %v", c.TrueColor, w)
	}
}
