package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	uv "github.com/charmbracelet/ultraviolet"
)

// goat's own windows (help, status, inventory, events) as set in
// ~/.config/goat/system.toml:
//
//	[help]
//	title = "❓Help"       # the title in its frame and status bar tab
//	activate = "alt+f1"   # the key that shows / hides it
//	theme = 32342         # its palette: the "id" from the palettes file
//	size = [40, 100%]     # width, height: columns / rows, or % of the screen
//	position = [-40, 0]   # left, top; negative: from the right / bottom edge
//
// Every key is optional; a window without a section opens as it always
// did. activate takes "f1" … "f12", "alt+f1" … "alt+f12", or Alt and a
// character: "alt+h", "alt-" (Alt+-). The defaults are Alt+F1 (help),
// Alt+F2 (status) and Alt+F3 (inventory): programs rarely use Alt with a
// function key, while F1 … F10 alone are theirs (mc, htop, …). A position
// has no effect in a direction the window fills (100%). One number is the
// left edge alone. "possition" is read as position too, and a percentage
// may be written without quotes.
//
// goat writes to it as the windows change: a palette chosen for one of
// them (Alt+t), and its size and position after it was moved or resized
// (a couple of seconds after the last change, or when it is hidden). Only
// the keys concerned are rewritten, in place; everything else in the file
// (comments, other keys and sections) stays as it is. goat checks the file
// every few seconds and picks up changes made by hand: titles, keys and
// palettes at once, size and position the next time the window is shown.

// systemFile is the file's name, in the config folder.
const systemFile = "system.toml"

// panelNames are goat's own windows, as system.toml's sections name them.
var panelNames = []string{"help", "status", "inventory", "events"}

// systemSaveDelay is how long a panel stays where it was moved before its
// place is written to system.toml.
const systemSaveDelay = 2 * time.Second

// dimension is one number of a size or position: columns / rows, or a
// percentage of the screen.
type dimension struct {
	v   float64
	pct bool
	set bool
}

// panelConf is one window's section.
type panelConf struct {
	title    string
	activate string // as written
	key      *panelKeySpec
	theme    int // a palette id (0: none)
	size     [2]dimension
	pos      [2]dimension
	posKey   string // "position", or "possition" as the file has it
}

// panelKeySpec is a key that shows / hides a panel.
type panelKeySpec struct {
	code rune // uv.KeyF1 … uv.KeyF12, or a character (always with alt)
	alt  bool
}

// mod is the modifier the key is pressed with.
func (k panelKeySpec) mod() uv.KeyMod {
	if k.alt {
		return uv.ModAlt
	}
	return 0
}

// lockMods are the lock keys' states a terminal may report with a key
// (kitty protocol); they don't make it another key.
const lockMods = uv.ModCapsLock | uv.ModNumLock | uv.ModScrollLock

// isFKey reports whether code is one of F1 … F12.
func isFKey(code rune) bool { return code >= uv.KeyF1 && code <= uv.KeyF12 }

func (k panelKeySpec) String() string {
	name := string(k.code)
	if isFKey(k.code) {
		name = fmt.Sprintf("F%d", k.code-uv.KeyF1+1)
	}
	if k.alt {
		return "Alt+" + name
	}
	return name
}

// defaultPanelKeys are the keys that show / hide goat's windows when
// system.toml doesn't say: Alt with F1, F2, F3.
var defaultPanelKeys = map[string]panelKeySpec{
	"help":      {code: uv.KeyF1, alt: true},
	"status":    {code: uv.KeyF2, alt: true},
	"inventory": {code: uv.KeyF3, alt: true},
}

// panelGeom is a panel's place on the screen.
type panelGeom struct{ x, y, w, h, cols, rows int }

// systemState is system.toml as read, and what was last written.
type systemState struct {
	path    string
	mod     time.Time
	size    int64
	checked time.Time
	err     string
	conf    map[string]panelConf
	// Each shown panel's place as last seen, and when it last changed
	// (zero: saved, or not changed by the user).
	geom    map[string]panelGeom
	changed map[string]time.Time
}

// systemRecheckEvery is how often the file is looked at for changes.
const systemRecheckEvery = 3 * time.Second

// parsePanelKey reads an activate value: "f1" … "f12", "alt+f1" …
// "alt+f12", "alt+x", "alt-".
func parsePanelKey(s string) (panelKeySpec, error) {
	low := strings.ToLower(strings.TrimSpace(s))
	alt := false
	if rest, ok := strings.CutPrefix(low, "alt"); ok && len(rest) > 2 && (rest[0] == '+' || rest[0] == '-') {
		if _, err := strconv.Atoi(rest[2:]); err == nil && rest[1] == 'f' { // alt+f1
			low, alt = rest[1:], true
		}
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(low, "f")); err == nil && strings.HasPrefix(low, "f") {
		if n < 1 || n > 12 {
			return panelKeySpec{}, fmt.Errorf("%q: F1 to F12", s)
		}
		return panelKeySpec{code: uv.KeyF1 + rune(n-1), alt: alt}, nil
	}
	if strings.HasPrefix(low, "alt") {
		rest := strings.TrimSpace(s)[3:]
		if utf8.RuneCountInString(rest) == 2 && (rest[0] == '+' || rest[0] == '-') {
			rest = rest[1:]
		}
		if r, size := utf8.DecodeRuneInString(rest); rest != "" && size == len(rest) && r > ' ' && !(r >= '0' && r <= '9') {
			return panelKeySpec{code: r, alt: true}, nil
		}
	}
	return panelKeySpec{}, fmt.Errorf("%q: want f1 … f12, alt+f1 … alt+f12, or alt and a character (alt+h, alt-)", s)
}

// quotePercents puts quotes around bare percentages (size = [40, 100%]),
// so the file reads as TOML. Text in quotes and comments is left alone.
func quotePercents(text string) string {
	var b strings.Builder
	for _, line := range strings.SplitAfter(text, "\n") {
		inStr := byte(0)
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch {
			case inStr != 0:
				if c == '\\' && inStr == '"' && i+1 < len(line) {
					b.WriteByte(c)
					i++
					c = line[i]
				} else if c == inStr {
					inStr = 0
				}
			case c == '"' || c == '\'':
				inStr = c
			case c == '#':
				b.WriteString(line[i:])
				i = len(line)
				continue
			case c == '-' || c == '.' || (c >= '0' && c <= '9'):
				j := i
				for j < len(line) && (line[j] == '-' || line[j] == '.' || (line[j] >= '0' && line[j] <= '9')) {
					j++
				}
				if j < len(line) && line[j] == '%' && (i == 0 || !isWordByte(line[i-1])) {
					b.WriteString(`"` + line[i:j+1] + `"`)
					i = j
					continue
				}
				b.WriteString(line[i:j])
				i = j - 1
				continue
			}
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isWordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// parseSystem reads system.toml's text. A mistake in a value leaves that
// value out (reported); text that isn't TOML is an error.
func parseSystem(text string) (map[string]panelConf, []string, error) {
	var raw map[string]any
	md, err := toml.Decode(quotePercents(text), &raw)
	if err != nil {
		return nil, nil, err
	}
	conf := map[string]panelConf{}
	var warns []string
	warn := func(f string, a ...any) { warns = append(warns, fmt.Sprintf(f, a...)) }
	for _, k := range md.Keys() {
		if len(k) != 1 {
			continue
		}
		name := k[0]
		sec, ok := raw[name].(map[string]any)
		if !slices.Contains(panelNames, name) || !ok {
			warn("[%s]: not one of goat's windows (%s)", name, strings.Join(panelNames, ", "))
			continue
		}
		pc := panelConf{posKey: "position"}
		for key, v := range sec {
			switch key {
			case "title":
				if s, ok := v.(string); ok {
					pc.title = cleanName(s)
				} else {
					warn("[%s] title: want text", name)
				}
			case "activate":
				s, _ := v.(string)
				spec, err := parsePanelKey(s)
				if err != nil {
					warn("[%s] activate %v", name, err)
					continue
				}
				pc.activate, pc.key = s, &spec
			case "theme", "palette":
				switch t := v.(type) {
				case int64:
					pc.theme = int(t)
				case string:
					if n, err := strconv.Atoi(t); err == nil {
						pc.theme = n
					} else {
						warn("[%s] theme %q: want a palette id (a number)", name, t)
					}
				default:
					warn("[%s] theme: want a palette id (a number)", name)
				}
			case "size":
				d, err := parseDimensions(v)
				if err != nil {
					warn("[%s] size: %v", name, err)
					continue
				}
				pc.size = d
			case "position", "possition":
				d, err := parseDimensions(v)
				if err != nil {
					warn("[%s] %s: %v", name, key, err)
					continue
				}
				pc.pos, pc.posKey = d, key
			default:
				warn("[%s] %s: unknown (title, activate, theme, size, position)", name, key)
			}
		}
		conf[name] = pc
	}
	return conf, warns, nil
}

// parseDimensions reads [a, b] (or [a], or a): each a number of columns /
// rows, or "N%".
func parseDimensions(v any) ([2]dimension, error) {
	var out [2]dimension
	list, ok := v.([]any)
	if !ok {
		list = []any{v}
	}
	if len(list) == 0 || len(list) > 2 {
		return out, errors.New("want [width, height] (or [left, top])")
	}
	for i, x := range list {
		switch t := x.(type) {
		case int64:
			out[i] = dimension{v: float64(t), set: true}
		case float64:
			out[i] = dimension{v: t, set: true}
		case string:
			n, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(t), "%"), 64)
			if err != nil || !strings.HasSuffix(strings.TrimSpace(t), "%") {
				return out, fmt.Errorf("%q: want a number or a percentage", t)
			}
			out[i] = dimension{v: n, pct: true, set: true}
		default:
			return out, fmt.Errorf("%v: want a number or a percentage", x)
		}
	}
	return out, nil
}

// cells is a dimension in cells, of a screen size of full.
func (d dimension) cells(full int) int {
	if d.pct {
		return int(d.v * float64(full) / 100)
	}
	return int(d.v)
}

// loadSystem reads system.toml if it changed. Reports whether it was read.
func (m *WM) loadSystem(now time.Time) bool {
	st := &m.sys
	st.checked = now
	if st.path == "" {
		return false
	}
	fi, err := os.Stat(st.path)
	if errors.Is(err, os.ErrNotExist) {
		if st.conf == nil && st.mod.IsZero() {
			st.conf = map[string]panelConf{}
			return false
		}
		st.conf, st.mod, st.size, st.err = map[string]panelConf{}, time.Time{}, 0, ""
		m.applySystem()
		return true
	}
	if err == nil && fi.ModTime().Equal(st.mod) && fi.Size() == st.size {
		return false
	}
	data, err := readSmallFile(st.path, 64<<10)
	if err == nil {
		st.mod, st.size = fi.ModTime(), fi.Size()
	}
	var conf map[string]panelConf
	var warns []string
	if err == nil {
		conf, warns, err = parseSystem(string(data))
	}
	if err != nil {
		msg := fmt.Sprintf("%s: %v (not used until fixed)", homeShort(st.path, m.home), err)
		if msg != st.err {
			st.err = msg
			m.logEvent(nil, evConfig, msg)
			logger.Warn("system.toml", "err", err)
		}
		return false
	}
	st.err = ""
	warns = append(warns, m.panelKeyCollisions(conf)...)
	for _, w := range warns {
		m.logEvent(nil, evConfig, homeShort(st.path, m.home)+": "+w)
		logger.Warn("system.toml", "problem", w)
	}
	st.conf = conf
	m.applySystem()
	return true
}

// panelKeyCollisions are warnings about activate keys that are also one
// of goat's shortcuts (the window wins: the shortcut is gone) or another
// window's key (the first in help, status, inventory, events wins).
func (m *WM) panelKeyCollisions(conf map[string]panelConf) []string {
	var warns []string
	seen := map[panelKeySpec]string{}
	for _, name := range panelNames {
		k := conf[name].key
		if k == nil {
			if d, ok := defaultPanelKeys[name]; ok {
				k = &d
			} else {
				continue
			}
		}
		if other, dup := seen[*k]; dup {
			warns = append(warns, fmt.Sprintf("[%s] activate: %s is the %s window's already (that one wins)", name, k, other))
			continue
		}
		seen[*k] = name
		if !k.alt || isFKey(k.code) || m.cfg == nil {
			continue
		}
		if act, ok := m.cfg.bindings[k.code]; ok && (act != actShowEvents || name != "events") {
			warns = append(warns, fmt.Sprintf("[%s] activate: %s is also %s, which it replaces", name, k, actionNames[act]))
		}
	}
	return warns
}

// refreshSystem re-reads system.toml every few seconds, and writes panel
// places that have settled. Reports whether something shown changed.
func (m *WM) refreshSystem(now time.Time) bool {
	changed := false
	if now.Sub(m.sys.checked) >= systemRecheckEvery {
		changed = m.loadSystem(now)
	}
	m.trackPanelGeometry(now, false)
	return changed
}

// applySystem gives the panels what the file says: titles and palettes
// at once; the place of a hidden one for the next time it is shown.
func (m *WM) applySystem() {
	for _, name := range panelNames {
		w := m.panelWindow(name)
		if w == nil {
			continue
		}
		m.applyPanelTheme(w, name)
		if !m.panelShown(w) {
			m.placePanel(w, name)
		}
	}
	m.dirty = true
}

// panelWindow is the panel of a name (nil if not made yet).
func (m *WM) panelWindow(name string) *Window {
	switch name {
	case "help":
		return m.help.win
	case "status":
		return m.statWin.win
	case "inventory":
		return m.inv.win
	case "events":
		return m.ev.win
	}
	return nil
}

// panelName is a panel's section name ("" if w isn't one).
func (m *WM) panelName(w *Window) string {
	for _, name := range panelNames {
		if w != nil && m.panelWindow(name) == w {
			return name
		}
	}
	return ""
}

// panelTitle is the panel's title: system.toml's, else its name.
func (m *WM) panelTitle(name string) string {
	if t := m.sys.conf[name].title; t != "" {
		return t
	}
	return name
}

// applyPanelTheme gives a panel its palette from system.toml (if any).
func (m *WM) applyPanelTheme(w *Window, name string) {
	id := m.sys.conf[name].theme
	if id == 0 {
		return
	}
	i := m.combos.byID(id)
	if i < 0 {
		if m.combos != nil {
			m.logEvent(nil, evConfig, fmt.Sprintf("%s: [%s] theme %d: no palette has that id", systemFile, name, id))
		}
		return
	}
	if w.pal != i+1 {
		m.setCombo(w, i)
	}
}

// placePanel puts a panel where system.toml says (as far as it says).
func (m *WM) placePanel(w *Window, name string) {
	pc, ok := m.sys.conf[name]
	if !ok {
		return
	}
	area := m.areaH()
	x, y, width, height := w.x, w.y, w.w, w.h
	if d := pc.size[0]; d.set && d.cells(m.cols) > 0 {
		width = min(d.cells(m.cols), m.cols)
	}
	if d := pc.size[1]; d.set && d.cells(area) > 0 {
		height = min(d.cells(area), area)
	}
	from := func(d dimension, full, size int) int {
		v := d.cells(full)
		if v < 0 {
			v += full
		}
		return clamp(v, 0, max(full-size, 0))
	}
	if d := pc.pos[0]; d.set {
		x = from(d, m.cols, width)
	} else if w.x > 0 && w.x+w.w == m.cols { // no position: keep to the right edge, as it opens
		x = max(m.cols-width, 0)
	}
	if d := pc.pos[1]; d.set {
		y = from(d, area, height)
	}
	if width >= m.cols {
		x = 0
	}
	if height >= area {
		y = 0
	}
	w.x, w.y, w.w, w.h = x, y, width, height
	if w.emu != nil { // (a new panel's emulator is made for this size)
		w.refit()
	}
}

// panelForKey is the panel a key shows / hides ("" for none): system.toml's
// activate keys, else Alt+F1 help, Alt+F2 status, Alt+F3 inventory. (The events window
// also has Alt+- , keys.show_events.)
func (m *WM) panelForKey(k uv.Key) string {
	r, isChar := bindingRune(k)
	for _, name := range panelNames {
		spec := m.panelActivateKey(name)
		if spec == nil {
			continue
		}
		switch {
		case spec.alt && isChar && k.Mod == uv.ModAlt && r == spec.code:
			return name
		case spec.alt && isChar && k.Mod == uv.ModAlt|uv.ModShift && r == spec.code:
			return name
		case isFKey(spec.code) && k.Code == spec.code && k.Mod&^lockMods == spec.mod():
			return name
		}
	}
	return ""
}

// panelActivateKey is the key that shows / hides a panel (nil: none of
// its own).
func (m *WM) panelActivateKey(name string) *panelKeySpec {
	if k := m.sys.conf[name].key; k != nil {
		return k
	}
	if k, ok := defaultPanelKeys[name]; ok {
		return &k
	}
	return nil
}

// panelKeyName is the key that shows / hides a panel, for the help.
func (m *WM) panelKeyName(name string) string {
	var keys []string
	if k := m.panelActivateKey(name); k != nil {
		keys = append(keys, k.String())
	}
	if name == "events" {
		if ev := "Alt+" + m.cfg.Keys.ShowEvents; !slices.Contains(keys, ev) {
			keys = append(keys, ev)
		}
	}
	return strings.Join(keys, " / ")
}

// togglePanel shows or hides a panel by name.
func (m *WM) togglePanel(name string) {
	switch name {
	case "help":
		m.toggleHelp()
	case "status":
		m.toggleStatusWin()
	case "inventory":
		m.toggleInventory()
	case "events":
		m.toggleEvents()
	}
}

// ---- writing ----------------------------------------------------------------

// savePanelTheme writes a panel's palette to system.toml.
func (m *WM) savePanelTheme(w *Window) {
	name := m.panelName(w)
	if name == "" || m.sys.path == "" {
		return
	}
	var v *string
	if c := m.combos.get(w.pal - 1); c != nil {
		s := fmt.Sprintf("%d", c.id)
		v = &s
	}
	m.writeSystem(name, map[string]*string{"theme": v})
}

// trackPanelGeometry notices panels moved or resized, and writes their
// size and position once they have stayed put for systemSaveDelay (at
// once with now, e.g. when one is hidden). A change of the screen's size
// isn't the user's: it is noted, not saved.
func (m *WM) trackPanelGeometry(now time.Time, force bool) {
	st := &m.sys
	if st.geom == nil {
		st.geom, st.changed = map[string]panelGeom{}, map[string]time.Time{}
	}
	for _, name := range panelNames {
		w := m.panelWindow(name)
		if w == nil {
			continue
		}
		g := panelGeom{w.x, w.y, w.w, w.h, m.cols, m.rows}
		last, seen := st.geom[name]
		switch {
		case !seen || last.cols != g.cols || last.rows != g.rows:
			st.geom[name] = g
			delete(st.changed, name)
		case last != g:
			st.geom[name] = g
			st.changed[name] = now
		}
		if t, ok := st.changed[name]; ok && (force || now.Sub(t) >= systemSaveDelay) {
			delete(st.changed, name)
			m.savePanelGeometry(w, name)
		}
	}
}

// savePanelGeometry writes a panel's size and position: "100%" for a
// direction it fills, and a negative position for one that was given from
// the right / bottom edge (or touches it).
func (m *WM) savePanelGeometry(w *Window, name string) {
	if m.sys.path == "" {
		return
	}
	area := m.areaH()
	pc := m.sys.conf[name]
	num := func(size, full int) string {
		if size >= full {
			return "100%"
		}
		return strconv.Itoa(size)
	}
	at := func(v, size, full int, d dimension) string {
		if size >= full {
			return "0"
		}
		if (d.set && d.v < 0) || (!d.set && v > 0 && v+size == full) {
			return strconv.Itoa(v - full)
		}
		return strconv.Itoa(v)
	}
	size := fmt.Sprintf("[%s, %s]", num(w.w, m.cols), num(w.h, area))
	pos := fmt.Sprintf("[%s, %s]", at(w.x, w.w, m.cols, pc.pos[0]), at(w.y, w.h, area, pc.pos[1]))
	key := pc.posKey
	if key == "" {
		key = "position"
	}
	m.writeSystem(name, map[string]*string{"size": &size, key: &pos})
}

// writeSystem sets (or, with nil, removes) keys of a section of
// system.toml and reads it back.
func (m *WM) writeSystem(section string, keys map[string]*string) {
	if err := writeSectionKeys(m.sys.path, section, keys); err != nil {
		logger.Warn("saving system.toml", "file", m.sys.path, "err", err)
		m.setBarMessage("can't save "+systemFile+": "+err.Error(), 5*time.Second)
		return
	}
	m.loadSystem(time.Now())
}

// writeSectionKeys sets keys in [section] of a TOML file (made if missing),
// in place: a key's line keeps its comment; keys not there are added at
// the end of the section; a nil value removes the key. The rest of the
// file is kept as it is.
func writeSectionKeys(path, section string, keys map[string]*string) error {
	old, err := readSmallFile(path, 64<<10)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(old), "\n"), "\n")
	if len(old) == 0 {
		lines = []string{"# " + systemFile + ": goat's own windows (written by goat too)"}
	}
	header := func(l string) (string, bool) {
		l = strings.TrimSpace(l)
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = strings.TrimSpace(l[:i])
		}
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			return strings.TrimSpace(strings.Trim(l, "[]")), true
		}
		return "", false
	}
	start, end := -1, len(lines)
	for i, l := range lines {
		if name, ok := header(l); ok {
			if start >= 0 {
				end = i
				break
			}
			if name == section {
				start = i
			}
		}
	}
	if start < 0 {
		lines = append(lines, "", "["+section+"]")
		start, end = len(lines)-1, len(lines)
	}
	done := map[string]bool{}
	var body []string
	for _, l := range lines[start+1 : end] {
		key, _, isKey := strings.Cut(l, "=")
		key = strings.Trim(strings.TrimSpace(key), `"`)
		v, ours := keys[key]
		if !isKey || !ours || strings.HasPrefix(strings.TrimSpace(l), "#") {
			body = append(body, l)
			continue
		}
		done[key] = true
		if v == nil {
			continue // removed
		}
		indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		body = append(body, indent+key+" = "+*v+trailingComment(l))
	}
	// Keys not there yet: after the section's last line that isn't blank.
	last := len(body)
	for last > 0 && strings.TrimSpace(body[last-1]) == "" {
		last--
	}
	var add []string
	for _, k := range []string{"title", "activate", "theme", "size", "position", "possition"} {
		if v, ok := keys[k]; ok && v != nil && !done[k] {
			add = append(add, k+" = "+*v)
		}
	}
	body = append(body[:last], append(add, body[last:]...)...)
	out := append(append(slices.Clone(lines[:start+1]), body...), lines[end:]...)
	return writeFileAtomic(path, []byte(strings.Join(out, "\n")+"\n"), 0o644)
}

// trailingComment is a line's "  # comment" (with the space before it),
// if it has one outside quotes.
func trailingComment(l string) string {
	inStr := rune(0)
	for i, c := range l {
		switch {
		case inStr != 0:
			if c == inStr {
				inStr = 0
			}
		case c == '"' || c == '\'':
			inStr = c
		case c == '#':
			j := i
			for j > 0 && (l[j-1] == ' ' || l[j-1] == '\t') {
				j--
			}
			return l[j:]
		}
	}
	return ""
}
