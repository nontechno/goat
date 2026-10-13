package main

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Launch scripts: "goat -script FILE" opens windows when goat starts, as
// FILE says (scripting.md has the full description):
//
//	# comment (a line starting with #; so are empty lines)
//	option: wrap: off      # (comments are whole lines only)
//	cd: /srv
//	{
//		cd: app              # /srv/app
//		run: make watch      # a window, in /srv/app, not wrapping
//		{
//			cd: ../logs      # /srv/logs: relative to the block's start (/srv/app)
//			title: logs
//			run: tail -f x.log
//		}
//		cd: web              # /srv/web: relative to /srv, where this block began
//		run: npm start
//	}
//	run: htop              # in /srv again
//
// Settings (cd:, title:, option:) hold from where they are set to the end
// of their block, inner blocks included; each run: opens a window with the
// settings in effect there. A relative cd: is taken from the folder the
// block started in (at the top: the script's folder). A value in quotes
// keeps its spaces and braces: title: "build ", run: "echo {x}".

// appSpec is one window of a launch script: the settings in effect at a
// run: line.
type appSpec struct {
	line       int // the run: line, for messages
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

// scriptScope is a block's settings: base is the folder relative cd:s are
// taken from (the folder in effect when the block began), set the settings
// so far (set.dir "" = the folder goat was started in).
type scriptScope struct {
	base string
	set  appSpec
	line int // where the block began, for messages
}

func parseScript(text, baseDir, name string) ([]appSpec, []string) {
	var apps []appSpec
	var warns []string
	warn := func(line int, f string, a ...any) {
		warns = append(warns, fmt.Sprintf("%s:%d: %s", name, line, fmt.Sprintf(f, a...)))
	}
	stack := []scriptScope{{base: baseDir}}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		n := i + 1
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		cur := &stack[len(stack)-1]
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		case line == "{":
			base := cur.set.dir
			if base == "" {
				base = cur.base
			}
			stack = append(stack, scriptScope{base: base, set: cur.set, line: n})
			continue
		case line == "}":
			if len(stack) == 1 {
				warn(n, "} without a matching { (ignored)")
			} else {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		key, val = strings.ToLower(strings.TrimSpace(key)), unquote(strings.TrimSpace(val))
		if !ok {
			hint := ""
			if strings.HasPrefix(line, "[") {
				hint = " ([sections] aren't used: each run: opens a window, { } group settings)"
			}
			warn(n, "%q: want cd:, title:, run:, option:, { or }%s", line, hint)
			continue
		}
		switch key {
		case "cd":
			cur.set.dir = scriptDir(val, cur.base)
		case "title":
			cur.set.title = cleanTitle(val)
		case "run":
			app := cur.set
			app.line, app.run = n, val
			apps = append(apps, app)
		case "option", "options":
			if err := cur.set.setOption(val); err != nil {
				warn(n, "option %q: %v", val, err)
			}
		default:
			warn(n, "unknown key %q: want cd, title, run or option", key)
		}
	}
	for i := len(stack) - 1; i > 0; i-- {
		warn(stack[i].line, "{ without a matching } (closed at the end of the file)")
	}
	return apps, warns
}

// unquote removes one pair of quotes ("..." or '...') around a value, which
// keeps spaces at its ends and lets it hold braces.
func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] &&
		!strings.ContainsRune(s[1:len(s)-1], rune(s[0])) { // not "a" "b"
		return s[1 : len(s)-1]
	}
	return s
}

// scriptDir resolves a "cd:" directory: ~ is the home folder, a relative
// path is taken from base (the folder the block started in).
func scriptDir(d, base string) string {
	d = expandHome(d)
	if !filepath.IsAbs(d) {
		d = filepath.Join(base, d)
	}
	return filepath.Clean(d)
}

// cleanTitle keeps a title to one line of printable text (spaces kept:
// quotes are how a title gets spaces at its ends).
func cleanTitle(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
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
