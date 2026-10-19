package main

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// helpScreen is the help window's visible text.
func helpScreen(m *WM) string {
	scr := uv.NewScreen(m.cols, m.rows)
	scene{m}.Draw(scr, scr.Bounds())
	w := m.help.win
	var b strings.Builder
	for y := w.contentY(); y < w.contentY()+w.contentH(); y++ {
		for x := w.contentX(); x < w.contentX()+w.contentW(); x++ {
			if c := scr.CellAt(x, y); c != nil && !c.IsZero() {
				b.WriteString(c.Content)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestHelpToggle(t *testing.T) {
	m, _, a, b := wmWithBackground(t)
	m.cols, m.rows = 100, 40
	m.focus(b)
	key(m, uv.Key{Code: uv.KeyF1, Mod: uv.ModAlt})
	if !m.helpShown() || !m.isHelp(m.focused) {
		t.Fatal("F1 didn't show the help window, focused")
	}
	if text := helpScreen(m); !strings.Contains(text, "goat: keys and mouse") || !strings.Contains(text, "Alt+c") {
		t.Errorf("help window:\n%s", text)
	}
	all := strings.Join(m.helpLines(80), "\n")
	for _, want := range []string{"Windows", "new window", "Alt+F1", "Alt+o", "Alt+h", "move left", "Mouse", "Esc, then a key"} {
		if !strings.Contains(all, want) {
			t.Errorf("help lacks %q:\n%s", want, all)
		}
	}
	// It's a window: other windows can be used meanwhile.
	m.focus(a)
	if !m.helpShown() {
		t.Fatal("focusing another window hid the help")
	}
	// F1 again hides it, though it isn't focused.
	key(m, uv.Key{Code: uv.KeyF1, Mod: uv.ModAlt})
	if m.helpShown() || m.focused != a {
		t.Fatalf("F1 again: shown %v, focused %v", m.helpShown(), m.focused.id)
	}
	// Shown again where it was; Alt+x hides it too.
	m.help.win.x = 3
	key(m, uv.Key{Code: uv.KeyF1, Mod: uv.ModAlt})
	if !m.helpShown() || m.help.win.x != 3 {
		t.Fatalf("not back where it was: x=%d", m.help.win.x)
	}
	key(m, uv.Key{Code: 'x', Mod: uv.ModAlt})
	if m.helpShown() {
		t.Fatal("Alt+x didn't hide the help window")
	}
}

func TestHelpWindowIsAPanel(t *testing.T) {
	m, bg, a, b := wmWithBackground(t)
	m.cols, m.rows = 100, 40
	m.toggleHelp()
	w := m.help.win
	// Not numbered, not counted, not hidden by Alt+0, not swapped.
	if m.number(a) != 1 || m.number(b) != 2 || len(m.floatingWindows()) != 2 {
		t.Errorf("the help window is numbered or counted")
	}
	m.focus(bg)
	key(m, uv.Key{Code: '0', Mod: uv.ModAlt}) // hide the others
	if w.minimized || !m.helpShown() || !a.minimized {
		t.Errorf("Alt+0: help hidden %v, a hidden %v", w.minimized, a.minimized)
	}
	key(m, uv.Key{Code: '0', Mod: uv.ModAlt}) // back
	m.focus(w)
	key(m, uv.Key{Code: 's', Mod: uv.ModAlt})
	if m.bg != bg {
		t.Error("the help window was swapped with the background")
	}
	if m.tabName(w) != "help" {
		t.Errorf("tab %q", m.tabName(w))
	}
	// Keys scroll it; a paste goes nowhere.
	helpScreen(m)
	top := w.scroll
	key(m, uv.Key{Code: uv.KeyPgDown})
	if w.scroll >= top && top > 0 {
		t.Errorf("PageDown didn't scroll: %d → %d", top, w.scroll)
	}
	m.paste("rm -rf /")
	if !strings.Contains(m.barMsg, "takes no input") {
		t.Errorf("paste: %q", m.barMsg)
	}
}

// The help follows config.toml's key bindings and the window's width.
func TestHelpContents(t *testing.T) {
	c, warns := parseConfig("[keys]\nnew_window = \"y\"\nsettings = \"g\"\n")
	if len(warns) != 0 {
		t.Fatal(warns)
	}
	m := newWM(c, nil, 100, 40)
	m.toggleHelp()
	if all := strings.Join(m.helpLines(80), "\n"); !strings.Contains(all, "Alt+y") || !strings.Contains(all, "Alt+g") {
		t.Errorf("rebound keys not shown:\n%s", all)
	}
	text := helpScreen(m)
	// Narrow: laid out again, keys above their descriptions; nothing
	// wider than the window.
	m.help.win.w = 30
	m.help.win.refit()
	text = helpScreen(m)
	for _, line := range strings.Split(text, "\n") {
		if textWidth(line) > m.help.win.contentW() {
			t.Errorf("line wider than the window: %q", line)
		}
	}
	if !strings.Contains(text, "  Alt+y") {
		t.Errorf("narrow layout:\n%s", text)
	}
}

// F1 … F3 alone are the program's (mc's help, menu and view); goat's
// windows are Alt+F1 … Alt+F3.
func TestPlainFKeysReachPrograms(t *testing.T) {
	m, _, _, b := wmWithBackground(t)
	m.cols, m.rows = 100, 40
	b.in = newInputQueue()
	m.focus(b)
	for _, c := range []rune{uv.KeyF1, uv.KeyF2, uv.KeyF3} {
		key(m, uv.Key{Code: c})
	}
	if m.helpShown() || m.statusWinShown() || m.inventoryShown() {
		t.Fatal("a plain F-key showed one of goat's windows")
	}
	b.in.mu.Lock()
	got := string(b.in.buf)
	b.in.mu.Unlock()
	if got != "\x1bOP\x1bOQ\x1bOR" {
		t.Fatalf("program got %q", got)
	}
	key(m, uv.Key{Code: uv.KeyF3, Mod: uv.ModAlt})
	if !m.inventoryShown() {
		t.Fatal("Alt+F3 didn't show the inventory")
	}
}
