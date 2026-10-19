package main

import (
	"strings"
	"time"
)

// The help window (Alt+F1): every keyboard shortcut and mouse action, with the
// keys as config.toml (and keymap.toml) bind them. Shortcuts can be changed
// in it (keymap.go): ↑ ↓ choose one, Enter changes its key. It is a regular window without a program,
// like the events window: move it, resize it, scroll it (arrows,
// PageUp/PageDown, Home/End while it has the focus), select and copy from
// it; other windows can be used while it is open. Alt+F1 shows it (focused),
// and Alt+F1 again hides it, whether it has the focus or not; Alt+x hides it
// too. Its contents are laid out for its width, again whenever it changes.

// helpState is the help window, what it was last laid out for, and the
// shortcut being changed in it (keymap.go).
type helpState struct {
	win    *Window
	shownW int // the content width the text was written for (0: not yet)

	sel       action            // the chosen shortcut (actNone: none)
	capturing bool              // waiting for its new key
	pending   *keyEdit          // a change waiting for Enter (a collision)
	msg       string            // what happened last
	rows      map[action][2]int // where each shortcut's lines are
	clickAt   time.Time         // the last click on a shortcut
}

// isHelp reports whether w is the help window.
func (m *WM) isHelp(w *Window) bool { return w != nil && w == m.help.win }

// helpShown reports whether the help window is on screen.
func (m *WM) helpShown() bool {
	return m.panelShown(m.help.win)
}

// toggleHelp is Alt+F1: show the help window (on top, focused, where it was last
// time), or hide it if it is on screen.
func (m *WM) toggleHelp() {
	if m.helpShown() {
		m.hidePanel(m.help.win)
		return
	}
	if m.help.win == nil {
		m.help.win = m.newPanelWindow("help", 500, placeRight)
	}
	m.help.shownW = 0 // lay out again (the key bindings may have changed too)
	m.showPanel(m.help.win)
}

// syncHelp writes the help text into the help window, laid out for its
// width, when it is shown for the first time or its width changed. Called
// before drawing.
func (m *WM) syncHelp() {
	w := m.help.win
	if w == nil || !m.helpShown() || w.contentW() == m.help.shownW {
		return
	}
	m.help.shownW = w.contentW()
	m.writePanel(w, m.helpLines(w.contentW()), true) // panels.go
}

// rewriteHelp lays the help out again (a change of the chosen shortcut,
// of a key, of the hint), keeping the view where it was; show brings the
// chosen shortcut into view.
func (m *WM) rewriteHelp(show bool) {
	w := m.help.win
	if w == nil || !m.helpShown() {
		return
	}
	m.help.shownW = w.contentW()
	m.writePanel(w, m.helpLines(w.contentW()), false)
	if r, ok := m.help.rows[m.help.sel]; ok && show {
		showPanelLines(w, max(r[0]-1, 0), r[1])
	}
	m.dirty = true
}

// helpSection is a heading and its rows.
type helpSection struct {
	title string
	rows  []helpRow
}

// helpRow is keys and what they do; a shortcut that can be changed has its
// action.
type helpRow struct {
	keys, desc string
	act        action
}

// helpText is everything the help window lists, with the keys as bound now.
func (m *WM) helpText() []helpSection {
	k := m.cfg.Keys
	alt := func(key string) string { return "Alt+" + key }
	sc := func(act action, key, desc string) helpRow { return helpRow{alt(key), desc, act} }
	fixed := func(keys, desc string) helpRow { return helpRow{keys, desc, actNone} }
	return []helpSection{
		{"Windows", []helpRow{
			sc(actNewWindow, k.NewWindow, "new window (in the active window's directory)"),
			sc(actFocusNext, k.FocusNext, "focus the next window"),
			sc(actFocusPrev, k.FocusPrev, "focus the previous window"),
			fixed("Alt+1 … 9", "focus window N (the number in its title)"),
			fixed("Alt+0", "focus the background shell"),
			fixed("Alt+0 again", "hide all floating windows (👻 in their tabs) / bring them back"),
			sc(actPinWindow, k.PinWindow, "pin / unpin: keep the window above the others"),
			sc(actSwapBackground, k.SwapBackground, "swap the window's program with the background's"),
			sc(actCloseWindow, k.CloseWindow, "close the window (ends its program); hides goat's own windows"),
			sc(actQuit, k.Quit, "quit goat (asks first while windows are open)"),
		}},
		{"Move and resize", []helpRow{
			sc(actMoveLeft, k.MoveLeft, "move left"),
			sc(actMoveDown, k.MoveDown, "move down"),
			sc(actMoveUp, k.MoveUp, "move up"),
			sc(actMoveRight, k.MoveRight, "move right"),
			fixed("Alt+arrows", "move"),
			sc(actResizeLeft, k.ResizeLeft, "narrower"),
			sc(actResizeDown, k.ResizeDown, "taller"),
			sc(actResizeUp, k.ResizeUp, "shorter"),
			sc(actResizeRight, k.ResizeRight, "wider"),
			fixed("Alt+Shift+arrows", "resize"),
		}},
		{"Colors", []helpRow{
			sc(actPalette, k.Palette, "choose the window's palette: background and text colors (Enter keeps it, Esc doesn't)"),
		}},
		{"History and long lines", []helpRow{
			fixed("Alt+PageUp/PageDown", "scroll the history by a page"),
			fixed("Alt+Shift+PageUp/Down", "scroll by 3 lines"),
			fixed("Alt+Home/End", "the oldest line / back to the live view"),
			sc(actToggleWrap, k.ToggleWrap, "wrap long lines, or not ([] wrapping, [) not)"),
			sc(actScrollLeft, k.ScrollLeft, "not wrapping: view left"),
			sc(actScrollRight, k.ScrollRight, "not wrapping: view right"),
		}},
		{"goat", []helpRow{
			fixed(m.panelKeyName("help"), "this help (again: hides it)"),
			fixed(m.panelKeyName("status"), "goat's status: version, windows, memory, CPU (again: hides it)"),
			fixed(m.panelKeyName("inventory"), "inventory: every window, its programs, memory, status (again: hides it)"),
			sc(actShowEvents, k.ShowEvents, "events window: bells, notifications, exits, … (again: hides it)"),
			sc(actSettings, k.Settings, "settings (~/.config/goat/local.toml)"),
			sc(actRepaint, k.Repaint, "repaint the screen"),
		}},
		{"In this window", []helpRow{
			fixed("↑ ↓", "choose a shortcut above"),
			fixed("Enter, double click", "change its key: press the new one (Esc: keep it)"),
			fixed("Delete", "back to its built-in key"),
			fixed("PageUp PageDown Home End", "scroll"),
		}},
		{"In the events, status and inventory windows", []helpRow{
			fixed("↑ ↓ PageUp PageDown", "scroll"),
			fixed("Home/End", "the top / the end"),
		}},
		{"In the inventory (" + m.panelKeyName("inventory") + ")", []helpRow{
			fixed("↑ ↓ Home/End", "choose a window"),
			fixed("Enter", "go to it (brings it back if hidden)"),
			fixed("click / double click", "choose / go to it"),
		}},
		{"Mouse", []helpRow{
			fixed("click", "focus a window (a status bar tab too)"),
			fixed("drag the title bar", "move the window"),
			fixed("drag an edge or corner", "resize the window"),
			fixed("drag over text", "select it; copied to the clipboard"),
			fixed("wheel", "scroll the history"),
			fixed("Shift+wheel", "not wrapping: view left / right"),
		}},
		{"Keys", []helpRow{
			fixed("Esc, then a key", "the same as Alt+key (within alt_timeout_ms)"),
			fixed("Option (macOS)", "works as Alt; goat -learn sets your terminal up"),
		}},
	}
}

// helpLines lays the help text out for a width: a column of keys and one
// of descriptions (wrapped under it), or keys above descriptions when the
// window is narrow. Under the title, a line says what to press (or what
// happened); the chosen shortcut is shown reversed. It records where each
// shortcut's lines are (help.rows).
func (m *WM) helpLines(width int) []string {
	const bold, plain, rev = "\x1b[1m", "\x1b[0m", "\x1b[7m"
	keyW := 0
	for _, s := range m.helpText() {
		for _, r := range s.rows {
			keyW = max(keyW, textWidth(r.keys))
		}
	}
	keyW = min(keyW, 30)
	twoCols := width >= keyW+2+24
	descW := max(width-keyW-2, 10)
	lines := []string{bold + "goat: keys and mouse" + plain}
	hint := m.helpHint()
	style := "\x1b[2m" // dim, when it only says what to press
	if m.help.pending != nil || m.help.capturing || m.help.msg != "" {
		style = bold
	}
	for _, l := range wrapWords(hint, max(width, 10)) {
		lines = append(lines, style+fitStart(l, width)+plain)
	}
	lines = append(lines, "")
	m.help.rows = map[action][2]int{}
	for _, s := range m.helpText() {
		lines = append(lines, bold+s.title+plain)
		for _, r := range s.rows {
			first := len(lines)
			if !twoCols {
				lines = append(lines, "  "+r.keys)
				for _, d := range wrapWords(r.desc, max(width-4, 10)) {
					lines = append(lines, "    "+d)
				}
			} else {
				key := r.keys
				pad := keyW - textWidth(key)
				if pad < 0 { // a long key gets its own line
					lines = append(lines, key)
					key, pad = "", keyW
				}
				for i, d := range wrapWords(r.desc, descW) {
					if i == 0 {
						lines = append(lines, key+strings.Repeat(" ", pad)+"  "+d)
					} else {
						lines = append(lines, strings.Repeat(" ", keyW+2)+d)
					}
				}
			}
			if r.act == actNone {
				continue
			}
			m.help.rows[r.act] = [2]int{first, len(lines) - 1}
			if r.act == m.help.sel { // the chosen shortcut, reversed across the window
				for i := first; i < len(lines); i++ {
					lines[i] = rev + lines[i] + strings.Repeat(" ", max(width-textWidth(lines[i]), 0)) + plain
				}
			}
		}
		lines = append(lines, "")
	}
	lines = append(lines, "More: README.md, aliasing.md, scripting.md. Changed shortcuts are kept in ~/.config/goat/keymap.toml.")
	return lines
}
