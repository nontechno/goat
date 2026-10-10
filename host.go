package main

import (
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

	uv "github.com/charmbracelet/ultraviolet"
)

// maxHostLabel is the longest host name shown, in characters.
const maxHostLabel = 32

// localConfig is ~/.config/goat/local.toml: settings for this machine only,
// kept apart from config.toml so that one can be shared between machines.
type localConfig struct {
	Hostname string `toml:"hostname"` // the name shown in window frames
	Hotkey   string `toml:"hotkey"`   // (not used yet)
}

// localConfigPath is local.toml in goat's config directory.
func localConfigPath() string {
	dir, err := configDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "local.toml")
}

// hostLabel is the host name shown in window frames (show_host): the
// "hostname" from local.toml (trimmed, cut to 32 characters) if it sets
// one; otherwise the system's host name. source says which ("local.toml" or
// "system"; "" if neither gave a name).
func hostLabel(localPath string) (name, source string) {
	if n := readLocalHostname(localPath); n != "" {
		return n, "local.toml"
	}
	if n, err := os.Hostname(); err == nil {
		if n = cleanHostName(n); n != "" {
			return n, "system"
		}
	}
	return "", ""
}

// readLocalHostname returns the cleaned "hostname" from local.toml ("" if
// the file is missing, unreadable, not valid TOML or sets no name). It is
// read like any small file goat polls: a regular file only, never
// blocking, at most 64 KB.
func readLocalHostname(path string) string {
	if path == "" {
		return ""
	}
	data, err := readSmallFile(path, 64<<10)
	if err != nil {
		return ""
	}
	var lc localConfig
	if _, err := toml.Decode(string(data), &lc); err != nil {
		return ""
	}
	return cleanHostName(lc.Hostname)
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

// drawHost writes the host name into the left of the bottom border:
// └─ myhost ─────── wrap ─┘. When the window is too narrow for all of it,
// the name is shortened (keeping at least 4 columns) or left out; it never
// covers the wrap indicator.
func (m *WM) drawHost(scr uv.Screen, w *Window, st uv.Style) {
	if !m.showIdent() || w.background || w.frame == frameNone || m.isEvents(w) {
		return
	}
	name := m.frameLabel(w)
	if name == "" {
		return
	}
	start := w.x + 2       // └─␠host
	limit := w.x + w.w - 2 // stop before ─┘
	label := " " + wrapLabel(w) + " "
	if wx := w.x + w.w - 2 - len(label); wx >= w.x+2 {
		limit = wx - 1 // keep one ─ between the host and the wrap label
	}
	maxW := limit - start - 2 // the spaces around the name
	if maxW < 1 {
		return
	}
	cs := textCells(name, st, maxW)
	// A name cut to a few characters says nothing: show at least 4 columns
	// (or the whole name, when it is shorter), or nothing.
	if w := cellsWidth(cs); w == 0 || (w < 4 && w < cellsWidth(textCells(name, st, 1<<20))) {
		return
	}
	y := w.y + w.h - 1
	put(scr, start, y, runeCell(' ', st))
	end := putCells(scr, start+1, y, cs)
	put(scr, end, y, runeCell(' ', st))
}
