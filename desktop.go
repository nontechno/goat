package main

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// The background window's colors are kept in local.toml, so the "desktop"
// looks the same the next time goat starts:
//
//	[background_window]
//	bg = 17    # Midnight
//	fg = 230   # Cream
//
// goat writes this section whenever the background window's colors change
// (Alt+b / Alt+f on it, or Alt+s bringing another window's colors there),
// leaving the rest of the file as it is. "default" (or a missing key) is
// the terminal's own color.

const desktopSection = "background_window"

// desktopColors are the background window's colors.
type desktopColors struct {
	bg, fg color.Color
}

func (d desktopColors) same(o desktopColors) bool {
	return sameColor(d.bg, o.bg) && sameColor(d.fg, o.fg)
}

// readDesktopColors reads the [background_window] colors from local.toml.
// ok is false when there are none (no file, no section, or it can't be
// read; a problem is returned as a warning).
func readDesktopColors(path string) (d desktopColors, ok bool, warning string) {
	if path == "" {
		return d, false, ""
	}
	data, err := readSmallFile(path, 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return d, false, ""
	}
	if err != nil {
		return d, false, fmt.Sprintf("%s: %v", path, err)
	}
	var lc struct {
		Desktop *struct {
			Bg Color `toml:"bg"`
			Fg Color `toml:"fg"`
		} `toml:"background_window"`
	}
	if _, err := toml.Decode(string(data), &lc); err != nil {
		return d, false, fmt.Sprintf("%s: %v (background window colors not restored)", path, err)
	}
	if lc.Desktop == nil {
		return d, false, ""
	}
	return desktopColors{bg: lc.Desktop.Bg.C, fg: lc.Desktop.Fg.C}, true, ""
}

// writeDesktopColors puts d into local.toml's [background_window] section,
// replacing the section if it is there and keeping every other line (the
// host name, comments, ...) as it is.
func writeDesktopColors(path string, d desktopColors, names [2]string) error {
	old, err := readSmallFile(path, 64<<10)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	section := []string{"[" + desktopSection + "]",
		"# The background window's colors, saved by goat when they change.",
		colorLine("bg", d.bg, names[0]),
		colorLine("fg", d.fg, names[1]),
	}
	out := replaceSection(strings.Split(string(old), "\n"), desktopSection, section)
	return writeFileAtomic(path, []byte(out), 0o644)
}

func colorLine(key string, c color.Color, name string) string {
	v := colorString(c)
	if c == nil {
		v = `"default"`
	} else if strings.HasPrefix(v, "#") {
		v = `"` + v + `"`
	}
	line := key + " = " + v
	if name != "" {
		line = fmt.Sprintf("%-10s # %s", line, name)
	}
	return line
}

// replaceSection returns the TOML text lines with table [name] replaced by
// section (appended at the end if there is none).
func replaceSection(lines []string, name string, section []string) string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	isHeader := func(l string) bool {
		l = strings.TrimSpace(l)
		return strings.HasPrefix(l, "[")
	}
	isOurs := func(l string) bool {
		l = strings.TrimSpace(l)
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = strings.TrimSpace(l[:i])
		}
		return strings.ReplaceAll(l, " ", "") == "["+name+"]"
	}
	var out []string
	done := false
	for i := 0; i < len(lines); i++ {
		if !isOurs(lines[i]) {
			out = append(out, lines[i])
			continue
		}
		for i+1 < len(lines) && !isHeader(lines[i+1]) { // skip the old section
			i++
		}
		if !done {
			out = append(out, section...)
			out = append(out, "")
			done = true
		}
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	if !done {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, section...)
	}
	return strings.Join(out, "\n") + "\n"
}

// restoreDesktop gives the background window the saved colors (at startup,
// and when a fresh background shell starts), finding them in the .color
// lists so Alt+b / Alt+f go on from there, and making sure the text shows.
func (m *WM) restoreDesktop(w *Window) {
	if !m.deskKnown {
		return
	}
	w.bgColor, w.fgColor = m.desk.bg, m.desk.fg
	w.bgPick, w.fgPick = 0, 0
	if p := m.palette; p != nil {
		for i, c := range p.bgs {
			if sameColor(c.c, w.bgColor) {
				w.bgPick = i + 1
				break
			}
		}
		w.fgTone = m.backgroundTone(w)
		for i, c := range p.fgs[w.fgTone] {
			if sameColor(c.c, w.fgColor) {
				w.fgPick = i + 1
				break
			}
		}
	}
	m.fixText(w)
}

// saveDesktop writes the background window's colors to local.toml when they
// have changed since they were last saved (or read at startup).
func (m *WM) saveDesktop() {
	w := m.bg
	if m.localPath == "" || w == nil {
		return
	}
	d := desktopColors{bg: w.bgColor, fg: w.fgColor}
	if (m.deskKnown && d.same(m.desk)) || (!m.deskKnown && d.bg == nil && d.fg == nil) {
		return
	}
	var names [2]string
	if p := m.palette; p != nil {
		if w.bgPick > 0 && w.bgPick <= len(p.bgs) {
			names[0] = p.bgs[w.bgPick-1].name
		}
		if w.fgPick > 0 && w.fgPick <= len(p.fgs[w.fgTone]) {
			names[1] = p.fgs[w.fgTone][w.fgPick-1].name
		}
	}
	if err := writeDesktopColors(m.localPath, d, names); err != nil {
		logger.Warn("saving the background window's colors", "file", m.localPath, "err", err)
		m.setStatus("can't save the background window's colors: "+err.Error(), 5*time.Second)
		return
	}
	m.desk, m.deskKnown = d, true
}
