package main

import (
	"io"
	"strings"

	"github.com/charmbracelet/colorprofile"
)

// 24-bit color ("true color"): whether goat passes programs' exact RGB
// colors on to the terminal it runs in, or rounds them to the nearest of
// the 256 standard colors (which every terminal goat supports shows the
// same; macOS Terminal has no 24-bit color).
//
// config.toml's true_color decides:
//
//	"auto"  the terminal's environment says: COLORTERM=truecolor (or
//	        24bit), a TERM known for it (…-direct, kitty, wezterm,
//	        ghostty, alacritty, …), its terminfo (Tc / RGB); or a terminal
//	        known to have it: iTerm2 (TERM_PROGRAM=iTerm.app, or
//	        LC_TERMINAL=iTerm2, which ssh passes on where it doesn't pass
//	        COLORTERM), WezTerm, ghostty, VS Code, Windows Terminal.
//	"on"    always (when "auto" guesses wrong, e.g. over ssh)
//	"off"   never
//
// Programs in goat's windows are told the same: COLORTERM=truecolor when
// goat passes 24-bit colors on, no COLORTERM when it doesn't, so they
// choose colors the terminal will show as they meant them (their
// 256-color themes), as they would outside goat. Palettes (Alt+t) follow
// it too: exact colors, or their 256-color versions.

// trueColorTerminals are TERM_PROGRAM / LC_TERMINAL values of terminals
// with 24-bit color.
var trueColorTerminals = []string{"iTerm.app", "iTerm2", "WezTerm", "ghostty", "vscode",
	"Hyper", "Tabby", "WarpTerminal", "rio", "contour", "kitty", "alacritty"}

// hostTrueColor decides whether the terminal gets 24-bit colors, and why
// (for the log and the status window).
func hostTrueColor(setting string, out io.Writer, env []string) (bool, string) {
	switch setting {
	case "on":
		return true, "true_color = \"on\""
	case "off":
		return false, "true_color = \"off\""
	}
	get := func(k string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, k+"="); ok {
				return v
			}
		}
		return ""
	}
	term := get("TERM")
	inMux := strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux")
	if ct := strings.ToLower(get("COLORTERM")); (ct == "truecolor" || ct == "24bit") && !inMux {
		return true, "COLORTERM=" + ct
	}
	if strings.HasSuffix(term, "-direct") {
		return true, "TERM=" + term
	}
	for _, t := range []string{"kitty", "wezterm", "ghostty", "alacritty", "foot", "contour", "rio"} {
		if strings.Contains(term, t) {
			return true, "TERM=" + term
		}
	}
	if colorprofile.Detect(out, env) == colorprofile.TrueColor { // terminfo (Tc, RGB), tmux's settings
		return true, "terminfo / tmux"
	}
	for _, k := range []string{"TERM_PROGRAM", "LC_TERMINAL"} {
		v := get(k)
		for _, t := range trueColorTerminals {
			if strings.EqualFold(v, t) {
				return true, k + "=" + v
			}
		}
	}
	if get("WT_SESSION") != "" {
		return true, "Windows Terminal"
	}
	return false, "the terminal doesn't say it has 24-bit color (no COLORTERM): true_color = \"on\" if it has"
}

// colorProfile is the renderer's profile for the terminal: 24-bit, or the
// 256 colors (goat's own colors are all among those).
func colorProfile(trueColor bool) colorprofile.Profile {
	if trueColor {
		return colorprofile.TrueColor
	}
	return colorprofile.ANSI256
}
