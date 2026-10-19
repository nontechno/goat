package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/BurntSushi/toml"

	uv "github.com/charmbracelet/ultraviolet"
)

// maxHostLabel is the longest host name shown, in characters.
const maxHostLabel = 32

// localConfig is ~/.config/goat/local.toml: settings for this machine only,
// kept apart from config.toml so that one can be shared between machines.
//
// (Host names, and the aliases shown for them, are in alias.toml: alias.go.)
type localConfig struct {
	Hotkey string `toml:"hotkey"` // (not used yet)

	// The right end of the status bar: the clock, and the name of the host
	// goat runs on (its alias from alias.toml, if it has one). Both shown
	// unless set to false.
	ShowClock      *bool `toml:"show_clock"`
	ShowLaunchHost *bool `toml:"show_launch_host"`

	// Whether long lines wrap in windows at start (the background shell,
	// launch script windows without a wrap option): overrides config.toml's
	// wrap_lines on this machine. New windows (Alt+c) take the state of the
	// window they were opened from.
	WrapLines *bool `toml:"wrap_lines"`

	// The size of new windows (Alt+c, launch script windows without a size
	// option): "100x30" (columns x rows, the whole window, frame included,
	// like a script's size option), or a share of the screen, "60%x50%"
	// (the two can be mixed). Replaces config.toml's new_window_*_ratio.
	NewWindowSize string `toml:"new_window_size"`

	// Ask before quitting (Alt+q) while floating windows are open: true
	// (the default) or false (quit at once).
	ConfirmQuit *bool `toml:"confirm_quit"`

	// Quit goat when the last floating window closes (its program exits, or
	// Alt+x), leaving only the background shell. false by default. The
	// events window doesn't count.
	ExitOnLastWindow *bool `toml:"exit_on_last_window"`

	// The style of the palette names in the palette dialog (Alt+t):
	// Pedantic, Funny, Outrageous, Vulgar, Corporate, Geeky, Wholesome or
	// Sports. Saved by the dialog.
	PaletteLingo string `toml:"palette_lingo"`
}

// readLocalConfig reads local.toml (ok false if it is missing, unreadable
// or not valid TOML). It is read like any small file goat polls: a regular
// file only, never blocking, at most 64 KB.
func readLocalConfig(path string) (lc localConfig, ok bool) {
	if path == "" {
		return lc, false
	}
	data, err := readSmallFile(path, 64<<10)
	if err != nil {
		return lc, false
	}
	if _, err := toml.Decode(string(data), &lc); err != nil {
		return localConfig{}, false
	}
	return lc, true
}

// localConfigPath is local.toml in goat's config directory.
func localConfigPath() string {
	dir, err := configDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "local.toml")
}

// systemHostName is the system's host name, cleaned ("" if unknown). What
// is shown for it may be an alias (alias.toml [host]; WM.showHost).
func systemHostName() string {
	if n, err := os.Hostname(); err == nil {
		return cleanHostName(n)
	}
	return ""
}

// barRight is what the right end of the status bar shows (local.toml).
type barRight struct {
	clock bool
	host  string // "" = not shown
}

// readBarRight reads the status bar settings from local.toml: the clock and
// the host name, each shown unless set to false (and the host name only if
// there is one).
func readBarRight(path string) barRight {
	lc, _ := readLocalConfig(path)
	b := barRight{clock: lc.ShowClock == nil || *lc.ShowClock}
	if lc.ShowLaunchHost == nil || *lc.ShowLaunchHost {
		b.host = systemHostName() // (its alias is shown: render.go)
	}
	return b
}

// refreshBarRight re-reads the status bar settings every hostRecheckEvery
// (so an edit of local.toml shows within seconds); reports a change.
func (m *WM) refreshBarRight(now time.Time) bool {
	if m.barChecked.IsZero() || now.Sub(m.barChecked) < hostRecheckEvery {
		// (never read at start, as in tests: the defaults stay)
		return false
	}
	m.barChecked = now
	b := readBarRight(m.localPath)
	if b == m.bar {
		return false
	}
	m.bar = b
	return true
}

// cleanHostName trims spaces, drops control and other invisible characters
// (so the frame can't be broken by escape sequences), and cuts the name to
// maxHostLabel characters.
func cleanHostName(s string) string {
	s = strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxHostLabel {
		s = strings.TrimSpace(string(r[:maxHostLabel]))
	}
	return s
}

// drawHost writes the host name into the left of the bottom border, and the
// git repository and branch after it (show_git):
// └─ 🧑alice 💻myhost 📖nontechno/goat 🌿main ──[]─┘. When the window is too narrow for all of it,
// the name is shortened (keeping at least 4 columns) or left out; it never
// covers the wrap indicator.
func (m *WM) drawHost(scr uv.Screen, w *Window, st uv.Style) {
	if !(m.showIdent() || m.cfg.ShowGit) || w.background || w.frame == frameNone || m.isPanel(w) {
		return
	}
	name := m.frameLabel(w)
	if name == "" {
		return
	}
	start := w.x + 2       // └─␠host
	limit := w.x + w.w - 2 // stop before ─┘
	label := wrapLabel(w)
	if wx := w.x + w.w - 2 - textWidth(label); wx >= w.x+2 {
		limit = wx - 1 // keep one ─ between the host and the wrap label
	}
	maxW := limit - start - 2 // the spaces around the name
	if maxW < 1 {
		return
	}
	cs := textCells(name, st, maxW)
	// A name cut to a few characters says nothing: show at least 6 columns
	// (the 💻 / 🧑 symbol and 4 characters; or the whole label, when it is
	// shorter), or nothing.
	if w := cellsWidth(cs); w == 0 || (w < 6 && w < textWidth(name)) {
		return
	}
	y := w.y + w.h - 1
	put(scr, start, y, runeCell(' ', st))
	end := putCells(scr, start+1, y, cs)
	put(scr, end, y, runeCell(' ', st))
}
