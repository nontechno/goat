package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
)

// The settings dialog (Alt+o, keys.settings): edits the settings of
// ~/.config/goat/local.toml.
//
//	╭─ Settings: ~/.config/goat/local.toml ───────────────╮
//	│                                                       │
//	│  Host name            friendly                        │
//	│  Show clock           yes (default)                   │
//	│  Show host name       yes (default)                   │
//	│  Wrap long lines      default: config.toml's (no)     │
//	│  New window size      100x30                          │
//	│  Confirm quit         yes (default)                   │
//	│  Exit on last window  no (default)                    │
//	│                                                       │
//	│               > Save <      Discard                   │
//	│  ↑↓ choose  ←→/Space change  Enter edit  Esc discard  │
//	╰───────────────────────────────────────────────────────╯
//
// It reads local.toml each time it opens. Save writes the settings back
// (only these keys change: comments, other keys and goat's [sections] stay)
// and applies them at once; Discard (Esc) leaves without changing anything.
// While it is up, the windows' programs keep running and their output keeps
// showing behind it; only your keys, mouse and pastes go to the dialog, so
// the windows can't be typed into, moved or closed until it is gone.

// settingKind is how a setting is edited.
type settingKind int

const (
	settingChoice settingKind = iota // one of a few values (←→ / Space)
	settingText                      // typed text (Enter edits)
)

// settingItem is one row of the dialog.
type settingItem struct {
	key   string // the local.toml key
	label string
	kind  settingKind

	// Choices: the value written for each (nil = the key is removed, the
	// default applies), what is shown, which one is the default.
	choices []*bool
	shown   []string
	def     int
	choice  int

	// Text: the value ("" = the key is removed, the default applies), what
	// the default is, and the check a new value must pass.
	text     string
	defText  string
	validate func(string) (string, error) // the value as written, or why not
}

// settingsDialog is the dialog while it is up.
type settingsDialog struct {
	path  string
	items []settingItem
	sel   int // a row, or len(items) = Save, len(items)+1 = Discard

	editing bool   // typing into a text setting
	buf     string // what is typed
	err     string // why the last value or save was refused

	// Where things were drawn, for clicks.
	rows             []uv.Rectangle
	saveBtn, discBtn uv.Rectangle
}

func boolp(b bool) *bool { return &b }

// openSettings shows the dialog with local.toml's settings as they are now.
func (m *WM) openSettings() {
	path := m.localPath
	if path == "" {
		path = localConfigPath()
	}
	if path == "" {
		m.setStatus("no config folder: settings can't be saved", 5*time.Second)
		return
	}
	lc, _ := readLocalConfig(path)
	wrapDef := m.cfg.WrapLines
	if m.wrapFromConfig != nil {
		wrapDef = *m.wrapFromConfig
	}
	l := m.cfg.Layout
	yesNo := func(on bool) string { return map[bool]string{true: "yes", false: "no"}[on] }
	boolItem := func(key, label string, v *bool, def bool) settingItem {
		it := settingItem{key: key, label: label, kind: settingChoice,
			choices: []*bool{boolp(true), boolp(false)}, shown: []string{"yes", "no"}}
		it.def = map[bool]int{true: 0, false: 1}[def]
		it.choice = it.def
		if v != nil {
			it.choice = map[bool]int{true: 0, false: 1}[*v]
		}
		return it
	}
	wrap := settingItem{key: "wrap_lines", label: "Wrap long lines", kind: settingChoice,
		choices: []*bool{nil, boolp(true), boolp(false)},
		shown:   []string{"config.toml's (" + yesNo(wrapDef) + ")", "yes", "no"}}
	if lc.WrapLines != nil {
		wrap.choice = map[bool]int{true: 1, false: 2}[*lc.WrapLines]
	}
	d := &settingsDialog{path: path, items: []settingItem{
		boolItem("show_clock", "Show clock", lc.ShowClock, true),
		boolItem("show_launch_host", "Show host name", lc.ShowLaunchHost, true),
		wrap,
		{key: "new_window_size", label: "New window size", kind: settingText,
			text:    strings.TrimSpace(lc.NewWindowSize),
			defText: fmt.Sprintf("config.toml's (%g%%x%g%%)", l.NewWindowWidthRatio*100, l.NewWindowHeightRatio*100),
			validate: func(s string) (string, error) {
				s = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), " ", "")
				if s == "" {
					return "", nil
				}
				if _, err := parseWindowSize(s); err != nil {
					return "", err
				}
				return s, nil
			}},
		boolItem("confirm_quit", "Confirm quit", lc.ConfirmQuit, true),
		boolItem("exit_on_last_window", "Exit on last window", lc.ExitOnLastWindow, false),
	}}
	m.cancelEsc()
	m.settings = d
	m.dirty = true
}

func (m *WM) closeSettings() {
	m.settings = nil
	m.dirty = true
}

// settingsKey handles a key while the dialog is up: every key goes here.
func (m *WM) settingsKey(k uv.Key) {
	d := m.settings
	if d.editing {
		m.settingsEditKey(k)
		return
	}
	last := len(d.items) + 1
	r, isChar := bindingRune(k)
	switch {
	case k.Code == uv.KeyEscape:
		m.closeSettings()
	case isChar && k.Mod.Contains(uv.ModAlt) && string(r) == m.cfg.Keys.Settings: // Alt+o again
		m.closeSettings()
	case k.Code == uv.KeyUp, k.Code == uv.KeyTab && k.Mod.Contains(uv.ModShift):
		d.sel = (d.sel + last) % (last + 1)
	case k.Code == uv.KeyDown, k.Code == uv.KeyTab:
		d.sel = (d.sel + 1) % (last + 1)
	case k.Code == uv.KeyLeft, k.Code == uv.KeyRight, k.Code == uv.KeySpace:
		switch {
		case d.sel < len(d.items):
			d.change(d.sel, pick(k.Code == uv.KeyLeft, -1, 1))
		case k.Code != uv.KeySpace: // between the buttons
			d.sel = pick(d.sel == len(d.items), len(d.items)+1, len(d.items))
		}
	case k.Code == uv.KeyEnter:
		switch {
		case d.sel == len(d.items):
			m.saveSettings()
			return
		case d.sel == last:
			m.closeSettings()
			return
		default:
			d.change(d.sel, 1)
		}
	case isChar && k.Mod&^uv.ModShift == 0 && (r == 's' || r == 'S'):
		m.saveSettings()
		return
	}
	m.dirty = true
}

// change moves a choice setting by delta, or starts editing a text one.
func (d *settingsDialog) change(i, delta int) {
	it := &d.items[i]
	d.err = ""
	if it.kind == settingText {
		d.editing, d.buf = true, it.text
		return
	}
	n := len(it.choices)
	it.choice = ((it.choice+delta)%n + n) % n
}

// settingsEditKey handles a key while a text setting is being typed.
func (m *WM) settingsEditKey(k uv.Key) {
	d := m.settings
	it := &d.items[d.sel]
	switch {
	case k.Code == uv.KeyEscape: // back to the value before
		d.editing, d.err = false, ""
	case k.Code == uv.KeyEnter:
		v, err := it.validate(d.buf)
		if err != nil {
			d.err = err.Error()
			break
		}
		it.text, d.editing, d.err = v, false, ""
	case k.Code == uv.KeyBackspace:
		if _, size := utf8.DecodeLastRuneInString(d.buf); size > 0 {
			d.buf = d.buf[:len(d.buf)-size]
		}
	case k.Code == 'u' && k.Mod == uv.ModCtrl:
		d.buf = ""
	case k.Text != "" && k.Mod&^uv.ModShift == 0:
		d.typed(k.Text)
	}
	m.dirty = true
}

// typed adds text to what is being typed (printable characters only, up to
// 64 characters).
func (d *settingsDialog) typed(s string) {
	for _, r := range s {
		if unicode.IsPrint(r) && utf8.RuneCountInString(d.buf) < 64 {
			d.buf += string(r)
		}
	}
}

// settingsPaste pastes into the setting being typed (or nowhere).
func (m *WM) settingsPaste(text string) {
	if d := m.settings; d.editing {
		d.typed(strings.ReplaceAll(text, "\n", " "))
		m.dirty = true
	}
}

// settingsMouse handles a click while the dialog is up: on a row it selects
// it (and changes a choice, or starts typing); on a button it presses it.
func (m *WM) settingsMouse(ev uv.MouseEvent) {
	click, ok := ev.(uv.MouseClickEvent)
	if !ok || click.Button != uv.MouseLeft {
		return
	}
	d := m.settings
	p := uv.Pos(click.X, click.Y)
	switch {
	case p.In(d.saveBtn):
		m.saveSettings()
		return
	case p.In(d.discBtn):
		m.closeSettings()
		return
	}
	if d.editing {
		return
	}
	for i, r := range d.rows {
		if p.In(r) {
			d.sel = i
			d.change(i, 1)
			m.dirty = true
		}
	}
}

// saveSettings writes the settings to local.toml and applies them.
func (m *WM) saveSettings() {
	d := m.settings
	set := map[string]*string{}
	for _, it := range d.items {
		switch it.kind {
		case settingChoice:
			if v := it.choices[it.choice]; v != nil {
				s := fmt.Sprint(*v)
				set[it.key] = &s
			} else {
				set[it.key] = nil
			}
		case settingText:
			if it.text != "" {
				s := tomlString(it.text)
				set[it.key] = &s
			} else {
				set[it.key] = nil
			}
		}
	}
	if err := writeTopLevelKeys(d.path, set); err != nil {
		d.err = "not saved: " + err.Error()
		logger.Warn("saving settings", "file", d.path, "err", err)
		m.dirty = true
		return
	}
	logger.Info("settings saved", "file", d.path)
	m.applyLocalSettings(d.path)
	m.closeSettings()
	m.setStatus("settings saved to "+homeShort(d.path, m.home), 4*time.Second)
}

// applyLocalSettings puts local.toml's settings into effect now.
func (m *WM) applyLocalSettings(path string) {
	lc, _ := readLocalConfig(path)
	var warns []string
	m.local, warns = readLocalOpts(lc)
	for _, w := range warns {
		m.logEvent(nil, evConfig, w)
	}
	switch {
	case lc.WrapLines != nil:
		m.cfg.WrapLines = *lc.WrapLines
	case m.wrapFromConfig != nil:
		m.cfg.WrapLines = *m.wrapFromConfig
	}
	now := time.Now()
	m.bar, m.barChecked = readBarRight(path), now
	m.dirty = true
}

// drawSettings draws the dialog in the middle of the screen, over
// everything.
func (m *WM) drawSettings(scr uv.Screen) {
	d := m.settings
	width := min(64, m.cols)
	height := len(d.items) + 6 // borders, blank lines, buttons, hint
	if d.err != "" {
		height++
	}
	x0 := max((m.cols-width)/2, 0)
	y0 := max((m.rows-height)/2, 0)
	base, hint := m.drawDialogBox(scr, x0, y0, width, height, "Settings: "+homeShort(d.path, m.home))
	inner := max(width-4, 1)
	labelW := 0
	for _, it := range d.items {
		labelW = max(labelW, textWidth(it.label))
	}
	labelW += 2
	selSt := base
	selSt.Attrs |= uv.AttrReverse
	dim := hint

	d.rows = d.rows[:0]
	for i, it := range d.items {
		y := y0 + 2 + i
		st := base
		if i == d.sel {
			st = selSt
			for x := x0 + 1; x < x0+width-1; x++ {
				put(scr, x, y, runeCell(' ', st))
			}
		}
		putCells(scr, x0+2, y, textCells(it.label, st, inner))
		vx := x0 + 2 + labelW
		room := max(x0+width-2-vx, 1)
		var cells []uv.Cell
		switch {
		case d.editing && i == d.sel:
			cells = textCells(fitEnd(d.buf, room-1), base, room-1)
			cursor := base
			cursor.Attrs |= uv.AttrReverse
			cells = append(cells, *runeCell(' ', cursor))
		case it.kind == settingChoice:
			v := it.shown[it.choice]
			if it.choices[it.choice] == nil {
				v = "default: " + v
			} else if it.choice == it.def {
				v += " (default)"
			}
			cells = textCells(v, st, room)
		case it.text == "":
			dst := dim
			if i == d.sel {
				dst = selSt
			}
			cells = textCells("default: "+it.defText, dst, room)
		default:
			cells = textCells(it.text, st, room)
		}
		putCells(scr, vx, y, cells)
		d.rows = append(d.rows, uv.Rect(x0+1, y, width-2, 1))
	}

	by := y0 + 3 + len(d.items)
	if d.err != "" {
		errSt := base
		errSt.Fg = btnDangerBg
		errSt.Attrs |= uv.AttrBold
		putCells(scr, x0+2, by, textCells(fitStart(d.err, inner), errSt, inner))
		by++
	}
	s := m.buttonCells("Save", d.sel == len(d.items), false, base.Bg)
	c := m.buttonCells("Discard", d.sel == len(d.items)+1, false, base.Bg)
	gap := 4
	bx := x0 + (width-len(s)-gap-len(c))/2
	putCells(scr, bx, by, s)
	putCells(scr, bx+len(s)+gap, by, c)
	d.saveBtn = uv.Rect(bx, by, len(s), 1)
	d.discBtn = uv.Rect(bx+len(s)+gap, by, len(c), 1)

	keys := "↑↓ choose  ←→/Space change  Enter edit  s save  Esc discard"
	if d.editing {
		keys = "type the value  Enter accept  Esc undo  Ctrl+U clear"
	}
	keys = fitStart(keys, inner)
	putCells(scr, x0+max((width-textWidth(keys))/2, 2), by+1, textCells(keys, hint, inner))
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// writeTopLevelKeys sets top-level keys of a TOML file (before its first
// [section]): a key with a value is set (replaced where it is, else added
// before the first section); a key with nil is removed. Everything else in
// the file stays as it was. The file is created if it doesn't exist.
func writeTopLevelKeys(path string, set map[string]*string) error {
	old, err := readSmallFile(path, 64<<10)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(string(old), "\n")
	if len(old) == 0 {
		lines = []string{"# goat local configuration file", "# Location: ~/.config/goat/local.toml", ""}
	}
	first := len(lines) // the first [section] (or the end)
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "[") {
			first = i
			break
		}
	}
	keyOf := func(l string) string {
		k, _, ok := strings.Cut(l, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" || strings.HasPrefix(k, "#") {
			return ""
		}
		return k
	}
	done := map[string]bool{}
	var head []string
	for _, l := range lines[:first] {
		k := keyOf(l)
		v, ours := set[k]
		switch {
		case k == "" || !ours:
			head = append(head, l)
		case done[k]: // a repeat: drop it
		case v == nil:
			done[k] = true // removed: the default applies
		default:
			head = append(head, k+" = "+*v)
			done[k] = true
		}
	}
	// New keys go after the existing top-level text, in a stable order.
	var added []string
	for _, k := range settingKeyOrder {
		if v, ok := set[k]; ok && v != nil && !done[k] {
			added = append(added, k+" = "+*v)
		}
	}
	for len(head) > 0 && strings.TrimSpace(head[len(head)-1]) == "" {
		head = head[:len(head)-1]
	}
	out := head
	if len(added) > 0 {
		out = append(out, "")
		out = append(out, added...)
	}
	if first < len(lines) {
		out = append(out, "")
		out = append(out, lines[first:]...)
	}
	text := strings.Join(out, "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return writeFileAtomic(path, []byte(text), 0o644)
}

// settingKeyOrder is the order new keys are added to local.toml.
var settingKeyOrder = []string{"show_clock", "show_launch_host", "wrap_lines",
	"new_window_size", "confirm_quit", "exit_on_last_window", "palette_lingo"}
