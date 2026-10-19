package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// The example script2.txt (Windows line endings, a #! line, comments,
// nested blocks): each run: gets the settings in effect where it is, and
// the folders its comments expect.
func TestParseExampleScript(t *testing.T) {
	data, err := os.ReadFile("script2.txt")
	if err != nil {
		t.Skip("no script2.txt")
	}
	apps, warns := parseScript(string(data), "/scripts", "script2.txt")
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	type want struct {
		run, dir, title, theme string
		wrap                   bool
	}
	wants := []want{
		{"./aaa -bbb -ccc 111", "/111/222", "", "", false},
		{"./aaa -bbb -ddd {{{ 222", "/111/222", "", "", false},
		{"./aaa -bbb -ccc 333", "/111/333", "bbbbbbbb ", "dark", false},
		{"./aaa -bbb -ccc 444", "/333", "", "dark", false},
		{"./aaa -bbb -ccc 555", "/111", "", "", false},
	}
	if len(apps) != len(wants) {
		t.Fatalf("%d windows: %+v", len(apps), apps)
	}
	for i, w := range wants {
		a := apps[i]
		if a.run != w.run || a.dir != w.dir || a.title != w.title || a.theme != w.theme ||
			a.wrap == nil || *a.wrap != w.wrap {
			t.Errorf("window %d: run %q dir %q title %q theme %q wrap %v; want %+v",
				i+1, a.run, a.dir, a.title, a.theme, a.wrap, w)
		}
	}
}

// Settings last to the end of their block; inner blocks inherit and may
// override; a relative cd: is taken from where the block began.
func TestParseScriptBlocks(t *testing.T) {
	home, _ := os.UserHomeDir()
	apps, warns := parseScript(`
run: top
cd: logs
option: wrap: off
option: size: 100x30
{
	cd: a
	cd: b
	title: inner
	option: wrap: on
	run: one
	{
		cd: ../c
		option: theme: light
		run: two
	}
	run: three
}
run: four
cd: ~/x
run:
`, "/scripts", "s")
	if len(warns) != 0 {
		t.Fatal(warns)
	}
	type want struct {
		run, dir, title, theme string
		wrap                   string // "", "on", "off"
	}
	wrapOf := func(a appSpec) string {
		switch {
		case a.wrap == nil:
			return ""
		case *a.wrap:
			return "on"
		}
		return "off"
	}
	wants := []want{
		{"top", "", "", "", ""},                            // nothing set yet: goat's folder
		{"one", "/scripts/logs/b", "inner", "", "on"},      // cd: b replaces cd: a, both from /scripts/logs
		{"two", "/scripts/logs/c", "inner", "light", "on"}, // ../c from /scripts/logs/b
		{"three", "/scripts/logs/b", "inner", "", "on"},    // the inner block's changes are gone
		{"four", "/scripts/logs", "", "", "off"},           // and the outer block's
		{"", filepath.Join(home, "x"), "", "", "off"},      // run: alone: a shell
	}
	if len(apps) != len(wants) {
		t.Fatalf("%d windows", len(apps))
	}
	for i, w := range wants {
		a := apps[i]
		if a.run != w.run || a.dir != w.dir || a.title != w.title || a.theme != w.theme || wrapOf(a) != w.wrap {
			t.Errorf("window %d: run %q dir %q title %q theme %q wrap %q; want %+v",
				i+1, a.run, a.dir, a.title, a.theme, wrapOf(a), w)
		}
		if a.cols != 100 || a.rows != 30 {
			if i > 0 { // set after the first run:
				t.Errorf("window %d: size %dx%d", i+1, a.cols, a.rows)
			}
		}
	}
}

func TestParseScriptQuotesAndProblems(t *testing.T) {
	apps, warns := parseScript(`
title: "  spaced  "
run: "echo {}"
run: "my tool" --name "x y"
run: 'single {'
}
[app]
colour: red
option: wrap: maybe
option: color: red
{
run: inside
`, "/", "s")
	if len(apps) != 4 {
		t.Fatalf("%d windows", len(apps))
	}
	if apps[0].title != "  spaced  " || apps[0].run != "echo {}" || apps[1].run != `"my tool" --name "x y"` || apps[2].run != "single {" {
		t.Fatalf("quotes: %q %q %q %q", apps[0].title, apps[0].run, apps[1].run, apps[2].run)
	}
	want := []string{"s:6: } without", "s:7: \"[app]\"", "s:8: unknown key \"colour\"",
		"s:9: option \"wrap: maybe\"", "s:10: option \"color: red\"", "s:11: { without"}
	if len(warns) != len(want) {
		t.Fatalf("warnings: %q", warns)
	}
	for i, w := range want {
		if !strings.HasPrefix(warns[i], w) {
			t.Errorf("warning %d: %q, want %s...", i, warns[i], w)
		}
	}
	if !strings.Contains(warns[1], "[sections] aren't used") {
		t.Errorf("no hint for the old format: %q", warns[1])
	}
}

// wmForScript is a window manager with real shells (/bin/sh), whose output
// is processed as goat does.
func wmForScript(t *testing.T) *WM {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Shell = "/bin/sh"
	m := newWM(cfg, nil, 120, 40)
	m.dir = t.TempDir()
	m.combos = testCombos(t)
	t.Cleanup(m.closeAll)
	return m
}

// pump handles program output and exits until cond holds (or 5 seconds).
func pump(t *testing.T, m *WM, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
		if cond() {
			return
		}
		select {
		case msg := <-m.out:
			m.handlePty(msg)
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("timed out")
}

func screenText(w *Window) string {
	var b strings.Builder
	for y := range w.emu.Height() {
		for x := range w.emu.Width() {
			if c := w.emu.CellAt(x, y); c != nil && c.Content != "" {
				b.WriteString(c.Content)
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestLaunchApps(t *testing.T) {
	m := wmForScript(t)
	dirA, dirB := t.TempDir(), t.TempDir()
	apps, warns := parseScript(`
option: theme: light
{
	cd: `+dirA+`
	title: first
	option: wrap: off
	option: size: 60x12
	option: pos: 5,3
	run: echo "in $(pwd)"; sleep 30
}
{
	cd: `+dirB+`
	title: second
	option: wrap: on
	option: focus: on
	option: pin: on
	run: sleep 30
}
{
	cd: /no/such/folder
	run:
}
`, "/", "test.script")
	if len(warns) != 0 {
		t.Fatal(warns)
	}
	m.launchApps(apps, "test.script")
	if len(m.windows) != 3 {
		t.Fatalf("%d windows", len(m.windows))
	}
	byID := func(id int) *Window { // (pinning reorders m.windows)
		for _, w := range m.windows {
			if w.id == id {
				return w
			}
		}
		t.Fatalf("no window %d", id)
		return nil
	}
	a, b, c := byID(1), byID(2), byID(3)
	pump(t, m, func() bool { return strings.Contains(screenText(a), "in "+dirA) })

	if m.tabName(a) != "1:first" || m.tabName(b) != "2:second" {
		t.Fatalf("titles %q %q", m.tabName(a), m.tabName(b))
	}
	if !a.nowrap || b.nowrap {
		t.Fatalf("wrap: a nowrap %v, b nowrap %v", a.nowrap, b.nowrap)
	}
	if a.w != 60 || a.h != 12 || a.x != 5 || a.y != 3 {
		t.Fatalf("geometry %d,%d %dx%d", a.x, a.y, a.w, a.h)
	}
	// theme: light: the light palettes, one background after another.
	light := m.combos.byTheme[2]
	if a.pal != light[0]+1 || b.pal != light[3]+1 || a.bgColor == b.bgColor {
		t.Fatalf("theme palettes %d %d (%v %v)", a.pal, b.pal, a.bgColor, b.bgColor)
	}
	if !m.readable(a.fgColor, a.bgColor) {
		t.Fatalf("unreadable text %v on %v", a.fgColor, a.bgColor)
	}
	if m.focused != b || !b.pinned || a.pinned {
		t.Fatalf("focus %v, pinned %v %v", m.focused.id, a.pinned, b.pinned)
	}
	// The missing folder: reported, and the app starts in the launch folder.
	if !strings.Contains(m.status, "/no/such/folder") {
		t.Fatalf("status %q", m.status)
	}
	pump(t, m, func() bool { return c.currentDir() != "" })
	if want, _ := filepath.EvalSymlinks(m.dir); c.currentDir() != want {
		t.Fatalf("fallback dir %q, want %q", c.currentDir(), want)
	}
}

// A program that fails keeps its window, saying how it ended; Enter closes
// it. One that succeeds closes as usual.
func TestLaunchedAppHold(t *testing.T) {
	m := wmForScript(t)
	apps, _ := parseScript(`
run: echo oops; exit 3
run: true
option: hold: on
run: echo done
`, "/", "s")
	m.launchApps(apps, "s")
	failed, ok, kept := m.windows[0], m.windows[1], m.windows[2]
	pump(t, m, func() bool { return failed.held && kept.held && !containsWin(m, ok) })
	text := screenText(failed)
	if !strings.Contains(text, "oops") || !strings.Contains(text, "exit status 3") || !strings.Contains(text, "press Enter") {
		t.Fatalf("held window shows:\n%s", text)
	}
	if !strings.Contains(screenText(kept), "done") {
		t.Fatalf("hold: on window shows:\n%s", screenText(kept))
	}
	// Typing doesn't reach the ended program; Enter closes the window.
	m.focus(failed)
	_ = m.handleKey(uv.Key{Code: 'x', Text: "x"})
	if !containsWin(m, failed) {
		t.Fatal("closed by a letter")
	}
	_ = m.handleKey(uv.Key{Code: uv.KeyEnter})
	if containsWin(m, failed) {
		t.Fatal("Enter didn't close it")
	}
}

func containsWin(m *WM, w *Window) bool {
	for _, x := range m.windows {
		if x == w {
			return true
		}
	}
	return false
}

// Alt+c windows aren't held: when their shell exits, they close.
func TestNewWindowNotHeld(t *testing.T) {
	m := wmForScript(t)
	if err := m.newWindow(); err != nil {
		t.Fatal(err)
	}
	w := m.focused
	w.send([]byte("exit 5\n"))
	pump(t, m, func() bool { return !containsWin(m, w) })
}
