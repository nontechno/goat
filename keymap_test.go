package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// helpWM is a WM with the help window shown and focused, and a keymap.toml
// in a temporary folder.
func helpWM(t *testing.T) (*WM, string) {
	t.Helper()
	m, _, _, _ := wmWithBackground(t)
	m.cols, m.rows = 110, 50
	m.keymapPath = filepath.Join(t.TempDir(), keymapFile)
	m.keysBase = m.cfg.Keys
	key(m, uv.Key{Code: uv.KeyF1, Mod: uv.ModAlt})
	return m, m.keymapPath
}

func helpText(m *WM) string { return strings.Join(m.helpLines(m.help.win.contentW()), "\n") }

// Choose a shortcut, Enter, press the new key: applied at once and saved.
func TestHelpChangeKey(t *testing.T) {
	m, path := helpWM(t)
	key(m, uv.Key{Code: uv.KeyDown}) // new window
	if m.help.sel != actNewWindow {
		t.Fatalf("chose %v", m.help.sel)
	}
	if !strings.Contains(helpText(m), "\x1b[7mAlt+c") {
		t.Errorf("chosen row not shown:\n%s", helpText(m))
	}
	key(m, uv.Key{Code: uv.KeyEnter})
	if !m.help.capturing || !strings.Contains(helpText(m), "press the new key for new window") {
		t.Fatalf("not waiting for a key:\n%s", helpText(m))
	}
	key(m, uv.Key{Code: 'y', Mod: uv.ModAlt}) // Alt+y: free
	if m.help.capturing || m.cfg.Keys.NewWindow != "y" || m.cfg.bindings['y'] != actNewWindow || m.cfg.bindings['c'] != actNone {
		t.Fatalf("not applied: %q %v", m.cfg.Keys.NewWindow, m.cfg.bindings['c'])
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `new_window = "y"`) || strings.Contains(string(data), "focus_next") {
		t.Fatalf("keymap.toml:\n%s %v", data, err)
	}
	if !strings.Contains(helpText(m), "Alt+y") || !strings.Contains(helpText(m), "saved in") {
		t.Errorf("help not updated:\n%s", helpText(m))
	}
	// Alt+y is a shortcut now (the help window is focused: it is goat's).
	n := len(m.windows)
	_ = m.handleKey(uv.Key{Code: 'c', Mod: uv.ModAlt}) // the old key: nothing
	if len(m.windows) != n || m.focused != m.help.win {
		t.Error("the old key still works")
	}
}

// A key another shortcut has: a warning, Enter swaps them, Esc doesn't.
func TestHelpKeyCollision(t *testing.T) {
	m, path := helpWM(t)
	m.help.sel = actNewWindow
	key(m, uv.Key{Code: uv.KeyEnter})
	key(m, uv.Key{Code: 'w', Mod: uv.ModAlt}) // pin window's
	if m.help.pending == nil || m.cfg.Keys.NewWindow != "c" {
		t.Fatal("collision not caught")
	}
	if text := helpText(m); !strings.Contains(text, "Alt+w is pin window already") || !strings.Contains(text, "swap") {
		t.Errorf("no warning:\n%s", text)
	}
	key(m, uv.Key{Code: uv.KeyEscape})
	if m.help.pending != nil || m.cfg.Keys.NewWindow != "c" || m.cfg.Keys.PinWindow != "w" {
		t.Fatal("Esc changed something")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("saved without a change")
	}
	key(m, uv.Key{Code: uv.KeyEnter})
	key(m, uv.Key{Code: 'w', Mod: uv.ModAlt})
	key(m, uv.Key{Code: uv.KeyEnter}) // swap
	if m.cfg.Keys.NewWindow != "w" || m.cfg.Keys.PinWindow != "c" ||
		m.cfg.bindings['w'] != actNewWindow || m.cfg.bindings['c'] != actPinWindow {
		t.Fatalf("not swapped: %q %q", m.cfg.Keys.NewWindow, m.cfg.Keys.PinWindow)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `new_window = "w"`) || !strings.Contains(string(data), `pin_window = "c"`) {
		t.Errorf("keymap.toml:\n%s", data)
	}
	// Delete: back to the built-in key (c: pin window has it, so a swap).
	key(m, uv.Key{Code: uv.KeyDelete})
	key(m, uv.Key{Code: uv.KeyEnter})
	if m.cfg.Keys.NewWindow != "c" || m.cfg.Keys.PinWindow != "w" {
		t.Fatalf("Delete: %q %q", m.cfg.Keys.NewWindow, m.cfg.Keys.PinWindow)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "new_window") || strings.Contains(string(data), "pin_window") {
		t.Errorf("defaults still saved:\n%s", data)
	}
}

// Keys that can't be shortcuts are refused; a plain key is taken as
// Alt+key.
func TestHelpKeyRefusedAndWarned(t *testing.T) {
	m, _ := helpWM(t)
	m.help.sel = actRepaint
	key(m, uv.Key{Code: uv.KeyEnter})
	key(m, uv.Key{Code: '5', Mod: uv.ModAlt})
	if !m.help.capturing || !strings.Contains(m.help.msg, "can't be a shortcut") {
		t.Fatalf("digit taken: %q", m.help.msg)
	}
	key(m, uv.Key{Code: 'u', Text: "u"}) // the key alone
	if m.cfg.Keys.Repaint != "u" {
		t.Fatalf("plain key: %q", m.cfg.Keys.Repaint)
	}
}

// keymap.toml is read over config.toml's keys; mistakes are reported.
func TestLoadKeymap(t *testing.T) {
	path := filepath.Join(t.TempDir(), keymapFile)
	_ = os.WriteFile(path, []byte("[keys]\nnew_window = \"y\"\nquit = \"Q\"\nnope = \"z\"\nsettings = 5\n"), 0o644)
	c := DefaultConfig()
	warns := loadKeymap(path, c)
	if c.Keys.NewWindow != "y" || c.Keys.Quit != "Q" || c.bindings['y'] != actNewWindow || c.bindings['c'] != actNone {
		t.Errorf("not applied: %+v", c.Keys)
	}
	if len(warns) != 2 || !strings.Contains(strings.Join(warns, "\n"), "nope") {
		t.Errorf("warnings: %v", warns)
	}
	// A key two shortcuts get: the second keeps... nothing (reported).
	_ = os.WriteFile(path, []byte("[keys]\nnew_window = \"w\"\n"), 0o644)
	c = DefaultConfig()
	if warns := loadKeymap(path, c); len(warns) != 1 || !strings.Contains(warns[0], "already bound") {
		t.Errorf("duplicate: %v", warns)
	}
	if w := loadKeymap(filepath.Join(t.TempDir(), "none.toml"), DefaultConfig()); w != nil {
		t.Errorf("missing file: %v", w)
	}
	// Saved only what differs from config.toml's keys, and read back alike.
	c = DefaultConfig()
	base := c.Keys
	c.Keys.Palette, c.Keys.ToggleWrap = "g", "Z"
	if err := saveKeymap(path, c, base); err != nil {
		t.Fatal(err)
	}
	c2 := DefaultConfig()
	if warns := loadKeymap(path, c2); len(warns) != 0 || c2.Keys != c.Keys {
		t.Errorf("round trip: %v %+v", warns, c2.Keys)
	}
}

// A click chooses a shortcut; a second one starts changing it.
func TestHelpClick(t *testing.T) {
	m, _ := helpWM(t)
	w := m.help.win
	helpScreen(m)
	r := m.help.rows[actPalette]
	showPanelLines(w, r[0], r[1])
	helpScreen(m)
	y := w.contentY() + r[0] - (w.emu.ScrollbackLen() - w.scroll)
	click := uv.MouseClickEvent{X: w.contentX() + 2, Y: y, Button: uv.MouseLeft}
	m.handleMouse(click)
	m.handleMouse(uv.MouseReleaseEvent{X: click.X, Y: y, Button: uv.MouseLeft})
	if m.help.sel != actPalette || m.help.capturing {
		t.Fatalf("click: %v %v", m.help.sel, m.help.capturing)
	}
	m.handleMouse(click)
	if !m.help.capturing {
		t.Fatal("double click didn't start the change")
	}
}

// A change waiting for its key is dropped when the help window loses the
// focus: the next time it has it, keys are its own again.
func TestHelpCaptureEndsOnBlur(t *testing.T) {
	m, _ := helpWM(t)
	key(m, uv.Key{Code: uv.KeyDown})
	key(m, uv.Key{Code: uv.KeyEnter})
	if !m.help.capturing {
		t.Fatal("not waiting for a key")
	}
	m.focus(m.bg) // a click on another window
	if m.help.capturing || !strings.Contains(helpText(m), "not changed") {
		t.Fatalf("still waiting: %v", m.help.capturing)
	}
	m.focus(m.help.win)
	key(m, uv.Key{Code: 'y', Mod: uv.ModAlt})
	if m.cfg.Keys.NewWindow != "c" {
		t.Fatalf("a later key changed it: %q", m.cfg.Keys.NewWindow)
	}
	// A collision waiting for Enter: hiding the window drops it too.
	key(m, uv.Key{Code: uv.KeyEnter})
	key(m, uv.Key{Code: 'n', Mod: uv.ModAlt}) // focus next's key
	if m.help.pending == nil {
		t.Fatal("no collision")
	}
	key(m, uv.Key{Code: uv.KeyF1, Mod: uv.ModAlt})
	if m.help.pending != nil || m.cfg.Keys.NewWindow != "c" {
		t.Fatal("collision kept after hiding")
	}
}
