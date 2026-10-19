package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	uv "github.com/charmbracelet/ultraviolet"
)

// Changing goat's shortcuts in the help window (F1), kept in
// ~/.config/goat/keymap.toml:
//
//	[keys]
//	new_window = "t"   # new window
//	palette = "y"      # palette
//
// The names are config.toml's [keys] names; keys set here win over
// config.toml's. goat reads the file at start, and writes it whenever a key
// is changed in the help window: every shortcut that differs from
// config.toml's (or the built-in) key, so the file is all you changed.
//
// In the help window: ↑ ↓ choose a shortcut, Enter (or a double click)
// changes it: press the new key, Alt+key or the key alone (Esc: keep the
// old one). A key another shortcut has already is a collision: goat says
// which, and Enter swaps the two keys (Esc: neither changes). Delete puts
// the chosen shortcut back to its built-in key (with the same check).
// Alt+1 … 9 and Alt+0 are the windows' and can't be taken; a key that also
// shows one of goat's windows (system.toml's activate) is warned about too.

// keymapFile is the file's name, in the config folder.
const keymapFile = "keymap.toml"

// keymapPath is keymap.toml in goat's config folder ("" if there is none).
func keymapPath() string {
	dir, err := configDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, keymapFile)
}

// loadKeymap applies keymap.toml's keys to c (and binds them again). A
// missing file is no problem; mistakes in it are returned as warnings
// (that key keeps config.toml's).
func loadKeymap(path string, c *Config) []string {
	if path == "" {
		return nil
	}
	data, err := readSmallFile(path, 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return []string{fmt.Sprintf("%s: %v", path, err)}
	}
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return []string{fmt.Sprintf("%s: %v (not used)", path, err)}
	}
	keys := raw
	if t, ok := raw["keys"].(map[string]any); ok {
		keys = t
	}
	var warns []string
	specs := map[string]keySpec{}
	for _, s := range c.keySpecs() {
		specs[s.name] = s
	}
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v := keys[name]
		if name == "keys" {
			continue
		}
		s, ok := specs[name]
		if !ok {
			warns = append(warns, fmt.Sprintf("%s: %q isn't a shortcut (ignored)", path, name))
			continue
		}
		str, ok := v.(string)
		if !ok {
			warns = append(warns, fmt.Sprintf("%s: %s: want a key in quotes (ignored)", path, name))
			continue
		}
		*s.val = str
	}
	for _, w := range c.bindKeys() {
		warns = append(warns, path+": "+w)
	}
	return warns
}

// saveKeymap writes the shortcuts that differ from base (config.toml's
// keys) to keymap.toml.
func saveKeymap(path string, c *Config, base KeyConfig) error {
	var b strings.Builder
	b.WriteString("# keymap.toml: goat's Alt+key shortcuts as changed in the help window (F1).\n")
	b.WriteString("# goat writes this file; keys here win over config.toml's [keys].\n")
	b.WriteString("# One shortcut per line: name = \"key\" (names as in config.toml [keys]).\n\n[keys]\n")
	baseCfg := &Config{Keys: base}
	baseSpecs := baseCfg.keySpecs()
	for i, s := range c.keySpecs() {
		if *s.val == *baseSpecs[i].val {
			continue
		}
		line := s.name + " = " + tomlString(*s.val)
		fmt.Fprintf(&b, "%-26s # %s\n", line, actionNames[s.act])
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(b.String()), 0o644)
}

// keyEdit is a change being made in the help window.
type keyEdit struct {
	act      action // the shortcut
	key      rune   // its new key
	other    action // the shortcut that has that key already (actNone: none)
	warnings []string
}

// helpChoose moves the help window's choice by d shortcuts (from none: the
// first or last).
func (m *WM) helpChoose(d int) {
	specs := m.cfg.keySpecs()
	i := -1
	for j, s := range specs {
		if s.act == m.help.sel {
			i = j
		}
	}
	switch {
	case i < 0 && d > 0:
		i = 0
	case i < 0:
		i = len(specs) - 1
	default:
		i = clamp(i+d, 0, len(specs)-1)
	}
	m.help.sel = specs[i].act
	m.help.msg = ""
	m.rewriteHelp(true)
}

// helpKey handles the help window's own keys (it has the focus); false
// for the others (which scroll it).
func (m *WM) helpKey(k uv.Key) bool {
	h := &m.help
	if h.pending != nil {
		switch {
		case k.Code == uv.KeyEnter:
			m.applyKeyEdit(*h.pending)
		case k.Code == uv.KeyEscape:
			h.pending, h.msg = nil, "not changed"
			m.rewriteHelp(false)
		}
		return true
	}
	if k.Mod != 0 {
		return false
	}
	switch k.Code {
	case uv.KeyUp:
		m.helpChoose(-1)
	case uv.KeyDown:
		m.helpChoose(1)
	case uv.KeyEnter:
		if h.sel == actNone {
			m.helpChoose(1)
		}
		h.capturing = true
		h.msg = ""
		m.rewriteHelp(false)
	case uv.KeyDelete, uv.KeyBackspace:
		if s := m.cfg.keySpec(h.sel); s != nil {
			m.proposeKey(h.sel, []rune(s.def)[0])
		}
	default:
		return false
	}
	return true
}

// helpCapture takes the new key for the chosen shortcut (every key goes
// here while the help window waits for it).
func (m *WM) helpCapture(k uv.Key) {
	h := &m.help
	if k.Code == uv.KeyEscape && k.Mod == 0 {
		h.capturing, h.msg = false, "not changed"
		m.rewriteHelp(false)
		return
	}
	if m.macOption { // macOS Option+key types a character: the key it stands for
		if k2, ok := macOptionKey(k); ok {
			k = k2
		}
	}
	r, ok := bindingRune(k)
	if !ok || r == ' ' || (r >= '0' && r <= '9') || k.Mod.Contains(uv.ModCtrl) {
		h.msg = "that key can't be a shortcut: press a character key, with or without Alt (not a digit: Alt+0 … 9 are the windows'), or Esc"
		m.rewriteHelp(false)
		return
	}
	h.capturing = false
	m.proposeKey(h.sel, r)
}

// proposeKey changes act's key to r: at once if nothing else has it, else
// after Enter (a collision: the two swap).
func (m *WM) proposeKey(act action, r rune) {
	h := &m.help
	e := keyEdit{act: act, key: r, other: m.cfg.bindings[r]}
	if e.other == act {
		h.msg = fmt.Sprintf("%s is Alt+%c already", actionNames[act], r)
		m.rewriteHelp(false)
		return
	}
	if name := m.panelForKey(uv.Key{Code: r, Mod: uv.ModAlt}); name != "" {
		e.warnings = append(e.warnings, fmt.Sprintf("Alt+%c also shows the %s window (system.toml), which goes first", r, name))
	}
	if m.macOption && strings.ContainsRune("einu`", r) {
		e.warnings = append(e.warnings, fmt.Sprintf("Option+%c is a dead key on macOS: there it works as Esc, then %c", r, r))
	}
	if e.other == actNone && len(e.warnings) == 0 {
		m.applyKeyEdit(e)
		return
	}
	h.pending = &e
	m.rewriteHelp(false)
}

// applyKeyEdit makes a change (swapping keys with the shortcut that had
// the new key), binds the keys again and saves keymap.toml.
func (m *WM) applyKeyEdit(e keyEdit) {
	h := &m.help
	h.pending = nil
	s := m.cfg.keySpec(e.act)
	if s == nil {
		return
	}
	old := *s.val
	*s.val = string(e.key)
	msg := fmt.Sprintf("%s: Alt+%c", actionNames[e.act], e.key)
	if e.other != actNone {
		if o := m.cfg.keySpec(e.other); o != nil {
			*o.val = old
			msg += fmt.Sprintf(", %s: Alt+%s (swapped)", actionNames[e.other], old)
		}
	}
	for _, w := range m.cfg.bindKeys() {
		m.logEvent(nil, evConfig, w)
	}
	if m.keymapPath != "" {
		if err := saveKeymap(m.keymapPath, m.cfg, m.keysBase); err != nil {
			msg += " (not saved: " + err.Error() + ")"
			logger.Warn("saving keymap.toml", "err", err)
		} else {
			msg += " · saved in " + homeShort(m.keymapPath, m.home)
		}
	}
	logger.Info("shortcut changed", "action", actionNames[e.act], "key", string(e.key))
	m.setStatus(msg, 4*time.Second)
	h.msg = msg
	m.rewriteHelp(false)
}

// helpHint is the line under the help window's title: what to press, or
// what is going on.
func (m *WM) helpHint() string {
	h := &m.help
	switch {
	case h.pending != nil:
		e := h.pending
		var parts []string
		if e.other != actNone {
			parts = append(parts, fmt.Sprintf("Alt+%c is %s already. Enter: swap (%s gets Alt+%s)",
				e.key, actionNames[e.other], actionNames[e.other], *m.cfg.keySpec(e.act).val))
		}
		parts = append(parts, e.warnings...)
		if e.other == actNone {
			parts = append(parts, fmt.Sprintf("Enter: use Alt+%c for %s anyway", e.key, actionNames[e.act]))
		}
		return "⚠ " + strings.Join(parts, " · ") + " · Esc: keep as it was"
	case h.capturing:
		return fmt.Sprintf("press the new key for %s (Alt+key, or the key alone) · Esc: keep it", actionNames[h.sel])
	case h.msg != "":
		return h.msg
	}
	return "↑ ↓ choose a shortcut · Enter: change its key · Delete: back to its built-in key"
}

// helpClick chooses the shortcut whose line was clicked (line: absolute,
// as posAt has it); a second click soon after changes it.
func (m *WM) helpClick(line int) {
	n := panelLine(m.help.win, line)
	now := time.Now()
	for act, r := range m.help.rows {
		if n < r[0] || n > r[1] {
			continue
		}
		double := m.help.sel == act && now.Sub(m.help.clickAt) < multiClickTime
		m.help.clickAt = now
		m.help.sel, m.help.msg = act, ""
		if double {
			m.clearSelectionIn(m.help.win)
			m.drag = drag{}
			m.help.capturing = true
		}
		m.rewriteHelp(false)
		return
	}
}
