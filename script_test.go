package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// The example script.txt (Windows line endings, a #! line, comments).
func TestParseExampleScript(t *testing.T) {
	data, err := os.ReadFile("script.txt")
	if err != nil {
		t.Skip("no script.txt")
	}
	apps, warns := parseScript(string(data), "/base", "script.txt")
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	if len(apps) != 2 {
		t.Fatalf("%d apps", len(apps))
	}
	a, b := apps[0], apps[1]
	if a.dir != "/ddd/fff" || a.title != "aaaaaaa" || a.run != "./aaa -bbb -ccc" || a.wrap == nil || *a.wrap {
		t.Fatalf("first: %+v", a)
	}
	if b.dir != "/ddd/ggg" || b.title != "bbbbbbbb" || b.run != "./bbb -aaa -ccc" || b.wrap == nil || !*b.wrap || b.theme != "dark" {
		t.Fatalf("second: %+v", b)
	}
}

func TestParseScriptOptionsAndProblems(t *testing.T) {
	home, _ := os.UserHomeDir()
	apps, warns := parseScript(`
title: too early
[app]
cd: logs
cd: ~/x
option: size: 100x30
option: pos: 3,4
option: frame: none
option: pin: on
option: focus: yes
option: hold: on
option: bg: Midnight
option: fg: #ffd700
option: color: red
option: wrap: maybe
colour: x
[settings]
cd: /nowhere
[app]
cd: logs
option theme dark
`, "/scripts", "s")
	if len(apps) != 2 {
		t.Fatalf("%d apps", len(apps))
	}
	a := apps[0]
	if a.dir != filepath.Join(home, "x") || a.cols != 100 || a.rows != 30 || !a.hasPos || a.x != 3 || a.y != 4 ||
		a.frame == nil || *a.frame != frameNone || !a.pin || !a.focus || a.hold != holdAlways ||
		a.bg != "Midnight" || a.fg != "#ffd700" || a.wrap != nil {
		t.Fatalf("first: %+v", a)
	}
	if apps[1].dir != "/scripts/logs" { // relative to the script's folder
		t.Fatalf("relative cd: %q", apps[1].dir)
	}
	want := []string{"s:2:", "s:14: option \"color: red\"", "s:15: option \"wrap: maybe\"", "s:16: unknown key \"colour\"",
		"s:17: unknown section [settings]", "s:21:"}
	if len(warns) != len(want) {
		t.Fatalf("warnings: %q", warns)
	}
	for i, w := range want {
		if !strings.HasPrefix(warns[i], w) {
			t.Errorf("warning %d: %q, want %s...", i, warns[i], w)
		}
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
	m.palette = fullPalette(t)
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
[app]
cd: `+dirA+`
title: first
run: echo "in $(pwd)"; sleep 30
option: wrap: off
option: theme: light
option: size: 60x12
option: pos: 5,3

[app]
cd: `+dirB+`
title: second
run: sleep 30
option: wrap: on
option: theme: light
option: focus: on
option: pin: on

[app]
cd: /no/such/folder
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
	// theme: light: the light backgrounds in turn (Snow, then Ivory).
	if a.bgColor != ansi.IndexedColor(231) || b.bgColor != ansi.IndexedColor(230) {
		t.Fatalf("theme backgrounds %v %v", a.bgColor, b.bgColor)
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
[app]
run: echo oops; exit 3
[app]
run: true
[app]
run: echo done
option: hold: on
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
