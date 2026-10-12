package main

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Launch scripts: "goat -script FILE" opens a window for each app listed
// in FILE when goat starts.
//
//	# a comment: a line starting with # (or empty; the #! line too)
//	[app]
//	cd: ~/src/project
//	title: build
//	run: make watch
//	option: wrap: off
//
// cd: is the working directory (relative: to the script's folder), title:
// the window's title (instead of the program's name), run: the program and
// its arguments, run by your shell, option: one option (see scriptOptions).
// A # after a value is part of the value. An [app] without "run:" gets a
// shell. See scripting.md. Windows open in the order listed;
// the last one has the focus unless one says "option: focus: on".

// appSpec is one [app] of a launch script.
type appSpec struct {
	line       int // where the [app] starts, for messages
	dir        string
	title      string
	run        string
	wrap       *bool
	theme      string // "dark" / "light": a background of that tone
	bg, fg     string // a .color name, palette number or "#rrggbb"
	frame      *frameStyle
	pin, focus bool
	cols, rows int // window size (0: the usual)
	x, y       int
	hasPos     bool
	hold       holdMode
}

// holdMode says whether a window stays open after its program ends (showing
// how it ended, until Enter is pressed).
type holdMode int

const (
	holdFailed holdMode = iota // when it fails (the default for script apps)
	holdAlways
	holdNever
)

// scriptOptions describes the "option:" lines, for messages.
const scriptOptions = "wrap, theme, bg, fg, frame, pin, focus, size, pos, hold"

// loadScript reads and parses a launch script. A file that can't be read is
// an error; problems in it are warnings (the apps that make sense still
// start).
func loadScript(path string) ([]appSpec, []string, error) {
	data, err := readSmallFile(path, 1<<20)
	if err != nil {
		return nil, nil, fmt.Errorf("script %s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	apps, warns := parseScript(string(data), filepath.Dir(abs), filepath.Base(path))
	return apps, warns, nil
}

func parseScript(text, baseDir, name string) ([]appSpec, []string) {
	var apps []appSpec
	var warns []string
	warn := func(line int, f string, a ...any) {
		warns = append(warns, fmt.Sprintf("%s:%d: %s", name, line, fmt.Sprintf(f, a...)))
	}
	var cur *appSpec
	inOther := false // in a [section] that isn't [app]
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			sec := strings.ToLower(strings.TrimSpace(strings.Trim(line, "[]")))
			if !strings.HasSuffix(line, "]") || sec != "app" {
				warn(n, "unknown section %s (only [app]); ignored up to the next [app]", line)
				cur, inOther = nil, true
				continue
			}
			apps = append(apps, appSpec{line: n})
			cur, inOther = &apps[len(apps)-1], false
			continue
		}
		if inOther {
			continue
		}
		if cur == nil {
			warn(n, "%q is outside an [app] section (ignored)", line)
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		key, val = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(val)
		if !ok {
			warn(n, "%q: want \"cd:\", \"title:\", \"run:\" or \"option:\"", line)
			continue
		}
		switch key {
		case "cd":
			cur.dir = scriptDir(val, baseDir)
		case "title":
			cur.title = cleanTitle(val)
		case "run":
			cur.run = val
		case "option", "options":
			if err := cur.setOption(val); err != nil {
				warn(n, "option %q: %v", val, err)
			}
		default:
			warn(n, "unknown key %q: want cd, title, run or option", key)
		}
	}
	return apps, warns
}

// scriptDir resolves a "cd:" directory: ~ is the home folder, a relative
// path is relative to the script's folder.
func scriptDir(d, baseDir string) string {
	d = expandHome(d)
	if d != "" && !filepath.IsAbs(d) {
		d = filepath.Join(baseDir, d)
	}
	return d
}

// cleanTitle keeps a title to one line of printable text.
func cleanTitle(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s))
}

// setOption applies one "option: name: value".
func (a *appSpec) setOption(s string) error {
	name, val, ok := strings.Cut(s, ":")
	if !ok {
		name, val, ok = strings.Cut(s, "=")
	}
	name, val = strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(val)
	if !ok || val == "" {
		return fmt.Errorf("want \"name: value\" (options: %s)", scriptOptions)
	}
	onOff := func() (bool, error) {
		switch strings.ToLower(val) {
		case "on", "true", "yes", "1":
			return true, nil
		case "off", "false", "no", "0":
			return false, nil
		}
		return false, fmt.Errorf("want on or off")
	}
	switch name {
	case "wrap":
		b, err := onOff()
		if err != nil {
			return err
		}
		a.wrap = &b
	case "theme":
		switch v := strings.ToLower(val); v {
		case "dark", "light":
			a.theme = v
		default:
			return fmt.Errorf("want dark or light")
		}
	case "bg", "background":
		a.bg = val
	case "fg", "text", "foreground":
		a.fg = val
	case "frame":
		f, ok := parseFrame(strings.ToLower(val))
		if !ok {
			return fmt.Errorf("want full, compact or none")
		}
		a.frame = &f
	case "pin":
		b, err := onOff()
		if err != nil {
			return err
		}
		a.pin = b
	case "focus":
		b, err := onOff()
		if err != nil {
			return err
		}
		a.focus = b
	case "size": // COLSxROWS: the whole window, frame included
		c, r, ok := strings.Cut(strings.ToLower(val), "x")
		cols, err1 := strconv.Atoi(strings.TrimSpace(c))
		rows, err2 := strconv.Atoi(strings.TrimSpace(r))
		if !ok || err1 != nil || err2 != nil || cols < 1 || rows < 1 {
			return fmt.Errorf("want COLUMNSxROWS, e.g. 100x30")
		}
		a.cols, a.rows = cols, rows
	case "pos", "position": // X,Y: the top-left corner, 0,0 = the screen's
		xs, ys, ok := strings.Cut(val, ",")
		x, err1 := strconv.Atoi(strings.TrimSpace(xs))
		y, err2 := strconv.Atoi(strings.TrimSpace(ys))
		if !ok || err1 != nil || err2 != nil || x < 0 || y < 0 {
			return fmt.Errorf("want X,Y, e.g. 10,2")
		}
		a.x, a.y, a.hasPos = x, y, true
	case "hold":
		switch strings.ToLower(val) {
		case "failed", "error", "on-error":
			a.hold = holdFailed
		case "on", "always", "yes", "true":
			a.hold = holdAlways
		case "off", "never", "no", "false":
			a.hold = holdNever
		default:
			return fmt.Errorf("want on, off or failed")
		}
	default:
		return fmt.Errorf("unknown option (options: %s)", scriptOptions)
	}
	return nil
}

// launchApps opens a window for each app of a launch script. Problems are
// reported (status bar, events window, log); the other apps still start.
func (m *WM) launchApps(apps []appSpec, name string) {
	var focus *Window
	opened := 0
	toneNext := map[tone]int{} // theme: dark / light take turns through their backgrounds
	for _, a := range apps {
		where := fmt.Sprintf("%s:%d", name, a.line)
		problem := func(f string, args ...any) {
			msg := where + ": " + fmt.Sprintf(f, args...)
			logger.Warn("launch script", "msg", msg)
			m.logEvent(nil, evConfig, msg)
			m.setStatus(msg, 15*time.Second)
		}
		spec := openSpec{dir: m.dir, frame: m.cfg.frame, wrap: m.cfg.WrapLines, title: a.title,
			hold: a.hold}
		if a.dir != "" {
			if usableDir(a.dir) {
				spec.dir = a.dir
			} else {
				problem("cd: %s isn't a folder goat can open; starting in %s", a.dir, m.dir)
			}
		}
		if a.run != "" {
			spec.command = append(strings.Fields(m.shell()), "-c", a.run)
		}
		if a.frame != nil {
			spec.frame = *a.frame
		}
		if a.wrap != nil {
			spec.wrap = *a.wrap
		}
		spec.cols, spec.rows, spec.x, spec.y, spec.hasPos = a.cols, a.rows, a.x, a.y, a.hasPos

		w, err := m.openWindow(spec)
		if err != nil {
			problem("can't start %q: %v", a.run, err)
			continue
		}
		opened++
		w.pinned = a.pin
		if msg := m.scriptColors(w, a, toneNext); msg != "" {
			problem("%s", msg)
		}
		m.applyHostColors(w)
		if a.focus {
			focus = w
		}
		logger.Info("launch script: window opened", "at", where, "window", w.id, "run", a.run,
			"dir", spec.dir, "title", a.title)
	}
	if focus != nil {
		m.focus(focus)
		m.raise(focus)
	}
	if opened > 0 && m.status == "" {
		m.setStatus(fmt.Sprintf("%s: opened %d window%s", name, opened, pick(opened == 1, "", "s")), 4*time.Second)
	}
	m.dirty = true
}

// scriptColors gives w the colors its app asks for: "theme" a background of
// that tone (the next one for each window that asks), "bg" / "fg" a .color
// name, palette number or "#rrggbb". Without any, w has the colors a new
// window gets (new_window_next_colors). It returns a problem to report.
func (m *WM) scriptColors(w *Window, a appSpec, toneNext map[tone]int) string {
	var problems []string
	p := m.palette
	if a.theme != "" {
		t := map[string]tone{"dark": toneDark, "light": toneLight}[a.theme]
		var of []namedColor
		if p != nil {
			for _, c := range p.bgs {
				if c.tone == t {
					of = append(of, c)
				}
			}
		}
		if len(of) == 0 {
			problems = append(problems, fmt.Sprintf("theme: no %s backgrounds (the .color files: goat -install)", a.theme))
		} else {
			c := of[toneNext[t]%len(of)]
			toneNext[t]++
			w.bgColor, w.bgPick = c.c, m.bgPosition(c.c)
			w.fgColor, w.fgPick, w.fgTone = nil, 0, t
		}
	}
	if a.bg != "" {
		if c, err := m.namedColor(a.bg, true); err != nil {
			problems = append(problems, "bg: "+err.Error())
		} else {
			w.bgColor, w.bgPick = c, max(m.bgPosition(c), 0)
		}
	}
	if a.fg != "" {
		if c, err := m.namedColor(a.fg, false); err != nil {
			problems = append(problems, "fg: "+err.Error())
		} else {
			w.fgColor, w.fgTone = c, m.backgroundTone(w)
			w.fgPick = 0
			if p != nil {
				w.fgPick = max(colorPosition(c, p.fgs[w.fgTone]), 0)
			}
		}
	}
	if a.theme == "" && a.bg == "" && a.fg == "" {
		return "" // openWindow already gave it a new window's colors
	}
	asked := a.fg != ""
	if m.fixText(w) && asked {
		problems = append(problems, fmt.Sprintf("fg: %s wouldn't show on that background; using %s", a.fg, m.textName(w)))
	}
	return strings.Join(problems, "; ")
}

// namedColor finds a color by its .color file name (any case; backgrounds
// for bg, text colors for fg), or reads it as a palette number, "#rrggbb"
// or "default".
func (m *WM) namedColor(s string, background bool) (color.Color, error) {
	if p := m.palette; p != nil {
		lists := [][]namedColor{p.fgs[toneDark], p.fgs[toneLight]}
		if background {
			lists = [][]namedColor{p.bgs}
		}
		for _, l := range lists {
			for _, c := range l {
				if strings.EqualFold(c.name, s) {
					return c.c, nil
				}
			}
		}
	}
	c, err := parseColorString(s)
	if err != nil {
		return nil, fmt.Errorf("%q is neither a color name from the .color files nor a color (0-255, \"#rrggbb\")", s)
	}
	return c, nil
}

// exitSummary says how a held window's program ended.
func exitSummary(w *Window) (string, bool) {
	ps := w.cmd.ProcessState
	if ps == nil {
		return "ended", false
	}
	if ps.Success() {
		return "exited", true
	}
	return ps.String(), false
}

// holdWindow keeps w open after its program ended, saying how it ended;
// Enter (or Alt+x) closes it.
func (m *WM) holdWindow(w *Window, how string) {
	w.held = true
	_, _ = w.emu.WriteString("\r\n\x1b[0;7m [" + w.displayTitle("process") + " " + how +
		" - press Enter to close] \x1b[0m")
	w.scroll = 0
	m.dirty = true
}

// shouldHold says whether w stays open now that its program has ended.
func shouldHold(w *Window) (bool, string) {
	how, ok := exitSummary(w)
	switch w.hold {
	case holdAlways:
		return true, how
	case holdFailed:
		return w.scripted && !ok, how
	}
	return false, how
}
