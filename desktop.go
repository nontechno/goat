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
//	palette = 29673   # Spring Green on Midnight Navy
//	bg = 233
//	fg = 115
//
// palette is the palette's id (as in the palettes file): goat restores the
// palette by it, with its colors as goat shows them now (24-bit or 256
// colors, which may differ from the last time). bg and fg are its colors as
// they were, used when there is no palette (or it isn't in the file any
// more). goat writes this section whenever the background window's colors change
// (a palette chosen with Alt+t, or Alt+s bringing another window's there),
// leaving the rest of the file as it is. "default" (or a missing key) is
// the terminal's own color.

const desktopSection = "background_window"

// desktopColors are the background window's colors.
type desktopColors struct {
	bg, fg color.Color
	pal    int // the palette's id (0: none; an id is fg*256+bg, never 0 for a palette)
}

func (d desktopColors) same(o desktopColors) bool {
	return d.pal == o.pal && sameColor(d.bg, o.bg) && sameColor(d.fg, o.fg)
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
			Palette int   `toml:"palette"`
			Bg      Color `toml:"bg"`
			Fg      Color `toml:"fg"`
		} `toml:"background_window"`
	}
	if _, err := toml.Decode(string(data), &lc); err != nil {
		return d, false, fmt.Sprintf("%s: %v (background window colors not restored)", path, err)
	}
	if lc.Desktop == nil {
		return d, false, ""
	}
	return desktopColors{bg: lc.Desktop.Bg.C, fg: lc.Desktop.Fg.C, pal: lc.Desktop.Palette}, true, ""
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
		"# The background window's palette and colors, saved by goat when they change."}
	if d.pal != 0 {
		line := fmt.Sprintf("palette = %d", d.pal)
		if names[0] != "" {
			line = fmt.Sprintf("%-15s # %s", line, names[0])
		}
		section = append(section, line, colorLine("bg", d.bg, ""), colorLine("fg", d.fg, names[1]))
	} else {
		section = append(section, colorLine("bg", d.bg, names[0]), colorLine("fg", d.fg, names[1]))
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

// restoreDesktop gives the background window the saved palette (at
// startup, and when a fresh background shell starts), or the saved colors
// (with the palette they are, if any), making sure the text shows.
func (m *WM) restoreDesktop(w *Window) {
	if !m.deskKnown {
		return
	}
	if i := m.combos.byID(m.desk.pal); m.desk.pal != 0 && i >= 0 {
		c := m.combos.get(i)
		w.pal, w.bgColor, w.fgColor = i+1, m.combos.bg(c), m.combos.fg(c)
		m.fixText(w)
		return
	}
	w.bgColor, w.fgColor = m.desk.bg, m.desk.fg
	w.pal = m.findCombo(w.bgColor, w.fgColor) + 1
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
	var names [2]string
	if c := m.combos.get(w.pal - 1); c != nil { // the palette, and its plain name
		d.pal, names[0] = c.id, c.names[0]
	}
	if (m.deskKnown && d.same(m.desk)) || (!m.deskKnown && d.bg == nil && d.fg == nil) {
		return
	}
	if err := writeDesktopColors(m.localPath, d, names); err != nil {
		logger.Warn("saving the background window's colors", "file", m.localPath, "err", err)
		m.setBarMessage("can't save the background window's colors: "+err.Error(), 5*time.Second)
		return
	}
	m.desk, m.deskKnown = d, true
}
