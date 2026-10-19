package main

import "strings"

// The help window (F1): every keyboard shortcut and mouse action, with the
// keys as config.toml binds them. It is a regular window without a program,
// like the events window: move it, resize it, scroll it (arrows,
// PageUp/PageDown, Home/End while it has the focus), select and copy from
// it; other windows can be used while it is open. F1 shows it (focused),
// and F1 again hides it, whether it has the focus or not; Alt+x hides it
// too. Its contents are laid out for its width, again whenever it changes.

// helpState is the help window and what it was last laid out for.
type helpState struct {
	win    *Window
	shownW int // the content width the text was written for (0: not yet)
}

// isHelp reports whether w is the help window.
func (m *WM) isHelp(w *Window) bool { return w != nil && w == m.help.win }

// helpShown reports whether the help window is on screen.
func (m *WM) helpShown() bool {
	return m.panelShown(m.help.win)
}

// toggleHelp is F1: show the help window (on top, focused, where it was last
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

// helpSection is a heading and its (keys, what they do) rows.
type helpSection struct {
	title string
	rows  [][2]string
}

// helpText is everything the help window lists, with the keys as bound now.
func (m *WM) helpText() []helpSection {
	k := m.cfg.Keys
	alt := func(keys ...string) string { // "Alt+h/j/k/l"
		return "Alt+" + strings.Join(keys, "/")
	}
	return []helpSection{
		{"Windows", [][2]string{
			{alt(k.NewWindow), "new window (in the active window's directory)"},
			{alt(k.FocusNext, k.FocusPrev), "focus the next / previous window"},
			{"Alt+1 … 9", "focus window N (the number in its title)"},
			{"Alt+0", "focus the background shell"},
			{"Alt+0 again", "hide all floating windows (👻 in their tabs) / bring them back"},
			{alt(k.PinWindow), "pin / unpin: keep the window above the others"},
			{alt(k.SwapBackground), "swap the window's program with the background's"},
			{alt(k.CloseWindow), "close the window (ends its program)"},
			{alt(k.Quit), "quit goat (asks first while windows are open)"},
		}},
		{"Move and resize", [][2]string{
			{alt(k.MoveLeft, k.MoveDown, k.MoveUp, k.MoveRight), "move left / down / up / right"},
			{"Alt+arrows", "move"},
			{alt(k.ResizeLeft, k.ResizeDown, k.ResizeUp, k.ResizeRight), "narrower / taller / shorter / wider"},
			{"Alt+Shift+arrows", "resize"},
		}},
		{"Colors", [][2]string{
			{alt(k.Palette), "choose the window's palette: background and text colors (Enter keeps it, Esc doesn't)"},
		}},
		{"History and long lines", [][2]string{
			{"Alt+PageUp/PageDown", "scroll the history by a page"},
			{"Alt+Shift+PageUp/Down", "scroll by 3 lines"},
			{"Alt+Home/End", "the oldest line / back to the live view"},
			{alt(k.ToggleWrap), "wrap long lines, or not ([] wrapping, [) not)"},
			{alt(k.ScrollLeft, k.ScrollRight), "not wrapping: view left / right"},
		}},
		{"goat", [][2]string{
			{m.panelKeyName("help"), "this help (again: hides it)"},
			{m.panelKeyName("status"), "goat's status: version, windows, memory, CPU (again: hides it)"},
			{m.panelKeyName("inventory"), "inventory: every window, its programs, memory, status (again: hides it)"},
			{m.panelKeyName("events"), "events window: bells, notifications, exits, …"},
			{alt(k.Settings), "settings (~/.config/goat/local.toml)"},
			{alt(k.Repaint), "repaint the screen"},
		}},
		{"In goat's windows (events, help, status, inventory)", [][2]string{
			{"↑ ↓ PageUp PageDown", "scroll"},
			{"Home/End", "the top / the end"},
			{alt(k.CloseWindow), "hide the window"},
		}},
		{"In the inventory (F3)", [][2]string{
			{"↑ ↓ Home/End", "choose a window"},
			{"Enter", "go to it (brings it back if hidden)"},
			{"click / double click", "choose / go to it"},
		}},
		{"Mouse", [][2]string{
			{"click", "focus a window (a status bar tab too)"},
			{"drag the title bar", "move the window"},
			{"drag an edge or corner", "resize the window"},
			{"drag over text", "select it; copied to the clipboard"},
			{"wheel", "scroll the history"},
			{"Shift+wheel", "not wrapping: view left / right"},
		}},
		{"Keys", [][2]string{
			{"Esc, then a key", "the same as Alt+key (within alt_timeout_ms)"},
			{"Option (macOS)", "works as Alt; goat -learn sets your terminal up"},
		}},
	}
}

// helpLines lays the help text out for a width: a column of keys and one
// of descriptions (wrapped under it), or keys above descriptions when the
// window is narrow.
func (m *WM) helpLines(width int) []string {
	const bold, plain = "\x1b[1m", "\x1b[0m"
	keyW := 0
	for _, s := range m.helpText() {
		for _, r := range s.rows {
			keyW = max(keyW, textWidth(r[0]))
		}
	}
	keyW = min(keyW, 30)
	twoCols := width >= keyW+2+24
	descW := max(width-keyW-2, 10)
	lines := []string{bold + "goat: keys and mouse" + plain, ""}
	for _, s := range m.helpText() {
		lines = append(lines, bold+s.title+plain)
		for _, r := range s.rows {
			if !twoCols {
				lines = append(lines, "  "+r[0])
				for _, d := range wrapWords(r[1], max(width-4, 10)) {
					lines = append(lines, "    "+d)
				}
				continue
			}
			key := r[0]
			pad := keyW - textWidth(key)
			if pad < 0 { // a long key gets its own line
				lines = append(lines, key)
				key, pad = "", keyW
			}
			desc := wrapWords(r[1], descW)
			for i, d := range desc {
				if i == 0 {
					lines = append(lines, key+strings.Repeat(" ", pad)+"  "+d)
				} else {
					lines = append(lines, strings.Repeat(" ", keyW+2)+d)
				}
			}
		}
		lines = append(lines, "")
	}
	lines = append(lines, "More: README.md, aliasing.md, scripting.md. Keys are set in config.toml [keys].")
	return lines
}
