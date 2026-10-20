package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// The example from the request, typos and all.
const systemExample = `# ~/.config/goat/system.toml

[help]
title = "❓Help"	# title to use (upper frame)
activate = "f1"	# key to (de)activate
theme = 32342	# "id" part of palette from terminal-color-combinations.json
size = [40, 100%]	# width, height. absolute or percentage of host window
possition = [-40,0]	# position of top:left corner

[status]
title = "Status"
activate = "f2"
theme = 33212
size = [40, 100%]
possition = [-80,0]

[inventory]
title = "Status"
activate = "f3"
theme = 43212
size = [60%, 60%]
possition = [-100]

[events]
title = "Events"
activate = "alt-"
theme = 33282
size = [40, 100%]
possition = [30]
`

func TestQuotePercents(t *testing.T) {
	for in, want := range map[string]string{
		"size = [40, 100%]":           `size = [40, "100%"]`,
		"size = [60%, 60.5%] # 50%":   `size = ["60%", "60.5%"] # 50%`,
		`title = "100% sure"`:         `title = "100% sure"`,
		"size = [40, \"100%\"]":       "size = [40, \"100%\"]",
		"position = [-40, 0]\nx = 5%": "position = [-40, 0]\nx = \"5%\"",
	} {
		if got := quotePercents(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

func TestParseSystemExample(t *testing.T) {
	conf, warns, err := parseSystem(systemExample)
	if err != nil || len(warns) != 0 {
		t.Fatal(err, warns)
	}
	h := conf["help"]
	if h.title != "❓Help" || h.theme != 32342 ||
		h.size != [2]dimension{{40, false, true}, {100, true, true}} ||
		h.pos != [2]dimension{{-40, false, true}, {0, false, true}} || h.posKey != "possition" {
		t.Errorf("help: %+v", h)
	}
	if inv := conf["inventory"]; inv.pos[0] != (dimension{-100, false, true}) || inv.pos[1].set ||
		inv.size[0] != (dimension{60, true, true}) {
		t.Errorf("inventory: %+v", inv)
	}
	if ev := conf["events"]; ev.pos[0].v != 30 {
		t.Errorf("events: %+v", ev)
	}
	// Mistakes: reported, the rest used.
	conf, warns, err = parseSystem("[help]\nsize = [1,2,3]\ncolour = 1\nactivate = \"f99\"\ntitle = \"ok\"\n[nope]\nx = 1\n")
	if err != nil || len(warns) != 3 || conf["help"].title != "ok" { // (activate: ignored)
		t.Errorf("mistakes: %v %v %+v", err, warns, conf)
	}
	if _, warns, _ := parseSystem("[inventory]\npossition = [-100.0]\n"); len(warns) != 1 || !strings.Contains(warns[0], "comma") {
		t.Errorf("a dot for a comma: %v", warns)
	}
	if _, _, err := parseSystem("[help\n"); err == nil {
		t.Error("broken TOML accepted")
	}
}

// systemWM is a WM with palettes and system.toml holding text.
func systemWM(t *testing.T, text string) *WM {
	t.Helper()
	m, _, _, _ := paletteWM(t)
	m.cols, m.rows = 200, 41 // 40 rows for windows
	m.sys.path = filepath.Join(t.TempDir(), systemFile)
	if text != "" {
		if err := os.WriteFile(m.sys.path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m.loadSystem(time.Now())
	return m
}

func TestSystemPanels(t *testing.T) {
	text := strings.ReplaceAll(systemExample, "theme = 32342", "theme = 28649") // an id the palettes have
	text = strings.ReplaceAll(text, `activate = "f2"`, `activate = "f4"`)       // (ignored: the keys are fixed)
	m := systemWM(t, text)
	key(m, uv.Key{Code: uv.KeyF1, Mod: uv.ModAlt})
	h := m.help.win
	if !m.helpShown() || m.tabName(h) != "❓Help" {
		t.Fatalf("help: shown %v, title %q", m.helpShown(), m.tabName(h))
	}
	if h.w != 40 || h.h != 40 || h.x != 160 || h.y != 0 {
		t.Errorf("help geometry %d,%d %dx%d", h.x, h.y, h.w, h.h)
	}
	if c := m.combos.get(h.pal - 1); c == nil || c.id != 28649 {
		t.Errorf("help palette %d", h.pal)
	}
	// activate is ignored: F4 does nothing, Alt+F2 shows the status window.
	key(m, uv.Key{Code: uv.KeyF4})
	if m.statusWinShown() {
		t.Error("activate = \"f4\" was used")
	}
	key(m, uv.Key{Code: uv.KeyF2, Mod: uv.ModAlt})
	if !m.statusWinShown() || m.tabName(m.statWin.win) != "Status" || m.statWin.win.x != 120 {
		t.Errorf("Alt+F2: shown %v, %q, x %d", m.statusWinShown(), m.tabName(m.statWin.win), m.statWin.win.x)
	}
	// inventory: 60% x 60%, 100 from the right.
	key(m, uv.Key{Code: uv.KeyF3, Mod: uv.ModAlt})
	if w := m.inv.win; w.w != 120 || w.h != 24 || w.x != 80 {
		t.Errorf("inventory geometry %d,%d %dx%d", w.x, w.y, w.w, w.h)
	}
	// events: Alt+- (also config.toml's key), x 30.
	key(m, uv.Key{Code: '-', Mod: uv.ModAlt})
	if !m.eventsShown() || m.ev.win.x != 30 || m.tabName(m.ev.win) != "Events" {
		t.Errorf("events: shown %v x %d", m.eventsShown(), m.ev.win.x)
	}
	// The help lists the fixed keys.
	if all := strings.Join(m.helpLines(100), "\n"); !strings.Contains(all, "Alt+F2") || strings.Contains(all, "F4") {
		t.Errorf("help keys:\n%s", all)
	}
	// An unknown palette id is reported.
	found := false
	for _, e := range m.ev.list {
		found = found || strings.Contains(e.text, "33212")
	}
	if !found {
		t.Error("unknown palette id not reported")
	}
}

// A palette chosen for a panel, and its new place, are written to
// system.toml, in place.
func TestSystemSaves(t *testing.T) {
	m := systemWM(t, "# mine\n[help]\ntitle = \"H\"   # keep me\nsize = [40, 100%]   # width, height\n\n[status]\ntitle = \"S\"\n")
	m.toggleHelp()
	h := m.help.win
	m.openPalette()
	key(m, uv.Key{Code: uv.KeyDown})
	key(m, uv.Key{Code: uv.KeyEnter})
	id := m.combos.get(h.pal - 1).id
	data, _ := os.ReadFile(m.sys.path)
	text := string(data)
	if !strings.Contains(text, "title = \"H\"   # keep me") || !strings.Contains(text, "theme = "+strconv.Itoa(id)) ||
		!strings.Contains(text, "# mine") || !strings.Contains(text, "[status]\ntitle = \"S\"") {
		t.Fatalf("after the palette:\n%s", text)
	}
	if strings.Index(text, "theme =") > strings.Index(text, "[status]") {
		t.Errorf("theme not in [help]:\n%s", text)
	}

	// Moved: saved once it has stayed put a while, or when hidden.
	now := time.Now()
	m.trackPanelGeometry(now, false)
	_ = m.run(actMoveLeft)
	m.trackPanelGeometry(now.Add(time.Second), false)
	if data, _ := os.ReadFile(m.sys.path); strings.Contains(string(data), "position") {
		t.Fatal("saved before it settled")
	}
	m.trackPanelGeometry(now.Add(time.Second+systemSaveDelay), false)
	data, _ = os.ReadFile(m.sys.path)
	if !strings.Contains(string(data), "size = [40, 100%]   # width, height") ||
		!strings.Contains(string(data), "position = [159, 0]") {
		t.Fatalf("after moving:\n%s", data)
	}
	// What goat writes, it reads back the same.
	if _, warns, err := parseSystem(string(data)); err != nil || len(warns) != 0 {
		t.Fatal(err, warns)
	}
	_ = m.run(actResizeLeft)
	m.toggleHelp() // hidden: saved at once
	if data, _ := os.ReadFile(m.sys.path); !strings.Contains(string(data), "size = [39, 100%]") {
		t.Fatalf("after hiding:\n%s", data)
	}
	// A screen size change isn't saved.
	m.toggleHelp()
	m.trackPanelGeometry(now, false)
	before, _ := os.ReadFile(m.sys.path)
	m.cols = 150
	m.place(h, h.x, h.y, h.w, h.h)
	m.trackPanelGeometry(now.Add(time.Hour), true)
	if after, _ := os.ReadFile(m.sys.path); string(after) != string(before) {
		t.Errorf("saved after a screen resize:\n%s", after)
	}
}

// Changes made by hand are picked up.
func TestSystemReload(t *testing.T) {
	m := systemWM(t, "[help]\ntitle = \"one\"\n")
	m.toggleHelp()
	if m.tabName(m.help.win) != "one" {
		t.Fatal(m.tabName(m.help.win))
	}
	later := time.Now().Add(time.Minute)
	_ = os.WriteFile(m.sys.path, []byte("[help]\ntitle = \"two\"\n"), 0o644)
	_ = os.Chtimes(m.sys.path, later, later)
	m.refreshSystem(time.Now().Add(systemRecheckEvery))
	if m.tabName(m.help.win) != "two" {
		t.Fatalf("not reloaded: %q", m.tabName(m.help.win))
	}
	// Broken: reported once, the last good settings stay.
	_ = os.WriteFile(m.sys.path, []byte("[help\n"), 0o644)
	_ = os.Chtimes(m.sys.path, later.Add(time.Minute), later.Add(time.Minute))
	n := len(m.ev.list)
	m.refreshSystem(time.Now().Add(2 * systemRecheckEvery))
	m.refreshSystem(time.Now().Add(3 * systemRecheckEvery))
	if len(m.ev.list) != n+1 || m.tabName(m.help.win) != "two" {
		t.Errorf("broken file: %d new events, title %q", len(m.ev.list)-n, m.tabName(m.help.win))
	}
}
