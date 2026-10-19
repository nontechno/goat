package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestWriteTopLevelKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.toml")
	orig := `# goat local configuration file
# friendly hostname
hostname = "friendly"

# goat --learn
hotkey = ""
show_clock = true
show_clock = true

[background_window]
# The background window's colors, saved by goat when they change.
bg = "#1d1f21"
show_clock = "not this one"
`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	no, size := "false", `"100x30"`
	err := writeTopLevelKeys(path, map[string]*string{
		"show_clock":      &no,   // replaced (and its repeat dropped)
		"hostname":        nil,   // removed
		"new_window_size": &size, // added before [background_window]
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := `# goat local configuration file
# friendly hostname

# goat --learn
hotkey = ""
show_clock = false

new_window_size = "100x30"

[background_window]
# The background window's colors, saved by goat when they change.
bg = "#1d1f21"
show_clock = "not this one"
`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	// A new file.
	fresh := filepath.Join(t.TempDir(), "new.toml")
	yes := "true"
	if err := writeTopLevelKeys(fresh, map[string]*string{"confirm_quit": &yes}); err != nil {
		t.Fatal(err)
	}
	if lc, ok := readLocalConfig(fresh); !ok || lc.ConfirmQuit == nil || !*lc.ConfirmQuit {
		t.Errorf("new file: %+v", lc)
	}
}

func TestTomlString(t *testing.T) {
	for in, want := range map[string]string{
		`box`:      `"box"`,
		`a "b" \c`: `"a \"b\" \\c"`,
		"x\x01y":   `"x\u0001y"`,
		"ünï":      `"ünï"`,
	} {
		if got := tomlString(in); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}

// settingsWM is a WM with a local.toml holding a host name and a section.
func settingsWM(t *testing.T) (*WM, string) {
	t.Helper()
	m, _, _, _ := wmWithBackground(t)
	m.cols, m.rows = 80, 24
	path := filepath.Join(t.TempDir(), "local.toml")
	text := "hostname = \"friendly\" # (no longer read: kept as it is)\nnew_window_size = \"70x20\"\n\n[background_window]\nbg = \"#000000\"\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	m.localPath = path
	return m, path
}

func key(m *WM, k uv.Key) {
	_ = m.handleKey(k)
}

func typeText(m *WM, s string) {
	for _, r := range s {
		key(m, uv.Key{Code: r, Text: string(r)})
	}
}

// screenText is the whole screen as drawn, row by row.
func settingsScreen(m *WM) string {
	scr := uv.NewScreen(m.cols, m.rows)
	scene{m}.Draw(scr, scr.Bounds())
	var b strings.Builder
	for y := range m.rows {
		for x := 0; x < m.cols; x++ {
			if c := scr.CellAt(x, y); c != nil && !c.IsZero() {
				b.WriteString(c.Content)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestSettingsDialogSave(t *testing.T) {
	m, path := settingsWM(t)
	key(m, uv.Key{Code: 'o', Mod: uv.ModAlt})
	if m.settings == nil {
		t.Fatal("Alt+o didn't open the dialog")
	}
	screen := settingsScreen(m)
	for _, want := range []string{"Settings:", "70x20", "Show clock", "yes (default)",
		"default: config.toml's", "Exit on last window", "Save", "Discard"} {
		if !strings.Contains(screen, want) {
			t.Errorf("dialog doesn't show %q:\n%s", want, screen)
		}
	}
	// Keys reach the dialog, not the windows.
	key(m, uv.Key{Code: uv.KeySpace}) // Show clock: no
	key(m, uv.Key{Code: uv.KeyDown})  // Show host name
	key(m, uv.Key{Code: uv.KeyDown})  // Wrap long lines
	key(m, uv.Key{Code: uv.KeyRight}) // yes
	key(m, uv.Key{Code: uv.KeyRight}) // no
	key(m, uv.Key{Code: uv.KeyDown})  // New window size
	key(m, uv.Key{Code: uv.KeyEnter}) // edit
	typeText(m, "huge")
	key(m, uv.Key{Code: uv.KeyEnter})
	if !m.settings.editing || m.settings.err == "" {
		t.Fatal("an invalid size was accepted")
	}
	key(m, uv.Key{Code: 'u', Mod: uv.ModCtrl}) // clear
	typeText(m, "90x25")
	key(m, uv.Key{Code: uv.KeyEnter})
	if m.settings.editing || m.settings.err != "" {
		t.Fatalf("90x25 refused: %q", m.settings.err)
	}
	key(m, uv.Key{Code: uv.KeyDown}) // Confirm quit
	key(m, uv.Key{Code: uv.KeySpace})
	key(m, uv.Key{Code: 's'}) // save
	if m.settings != nil {
		t.Fatalf("not closed after saving: %q", m.settings.err)
	}
	data, _ := os.ReadFile(path)
	text := string(data)
	if strings.Count(text, "new_window_size") != 1 {
		t.Errorf("new_window_size twice:\n%s", text)
	}
	for _, want := range []string{`hostname = "friendly"`, "show_clock = false", "show_launch_host = true",
		"wrap_lines = false", `new_window_size = "90x25"`, "confirm_quit = false",
		"exit_on_last_window = false", "[background_window]\nbg = \"#000000\""} {
		if !strings.Contains(text, want) {
			t.Errorf("local.toml lacks %q:\n%s", want, text)
		}
	}
	// Applied at once.
	if m.bar.clock || m.cfg.WrapLines || m.local.confirmQuit ||
		m.local.newSize != (windowSize{cols: 90, rows: 25}) {
		t.Errorf("not applied: bar %+v wrap %v local %+v", m.bar, m.cfg.WrapLines, m.local)
	}
}

func TestSettingsDialogDiscard(t *testing.T) {
	m, path := settingsWM(t)
	before, _ := os.ReadFile(path)
	m.openSettings()
	key(m, uv.Key{Code: uv.KeyDown})
	key(m, uv.Key{Code: uv.KeySpace})  // a change ...
	key(m, uv.Key{Code: uv.KeyEscape}) // ... discarded
	if m.settings != nil {
		t.Fatal("Esc didn't close the dialog")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Errorf("file changed by Discard:\n%s", after)
	}
	if !m.bar.clock {
		t.Error("a discarded change was applied")
	}

	// Esc while typing undoes the typing only.
	m.openSettings()
	for range 3 {
		key(m, uv.Key{Code: uv.KeyDown}) // New window size
	}
	key(m, uv.Key{Code: uv.KeyEnter})
	typeText(m, "9")
	key(m, uv.Key{Code: uv.KeyEscape})
	if m.settings == nil || m.settings.items[3].text != "70x20" {
		t.Fatalf("Esc while typing: %+v", m.settings)
	}
	// A click on Discard.
	settingsScreen(m) // draws the buttons
	r := m.settings.discBtn
	m.handleMouse(uv.MouseClickEvent{X: r.Min.X + 1, Y: r.Min.Y, Button: uv.MouseLeft})
	if m.settings != nil {
		t.Fatal("Discard click didn't close")
	}
}

// Clearing a text setting removes the key: the default applies again.
func TestSettingsDialogBackToDefault(t *testing.T) {
	m, path := settingsWM(t)
	m.openSettings()
	for range 3 {
		key(m, uv.Key{Code: uv.KeyDown}) // New window size
	}
	key(m, uv.Key{Code: uv.KeyEnter})
	key(m, uv.Key{Code: 'u', Mod: uv.ModCtrl})
	key(m, uv.Key{Code: uv.KeyEnter})
	if !strings.Contains(settingsScreen(m), "default: config.toml's") {
		t.Error("the default isn't shown")
	}
	r := m.settings.saveBtn
	m.handleMouse(uv.MouseClickEvent{X: r.Min.X + 1, Y: r.Min.Y, Button: uv.MouseLeft})
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "new_window_size") {
		t.Errorf("new_window_size still set:\n%s", data)
	}
}

// While a dialog is up, the windows' programs keep running and their output
// keeps showing; only input goes to the dialog.
func TestDialogsKeepWindowsLive(t *testing.T) {
	for _, open := range []func(m *WM){
		func(m *WM) { m.openSettings() },
		func(m *WM) { m.askQuit() },
		func(m *WM) { m.combos, _ = loadCombos("testdata"); m.openPalette() },
	} {
		m, _ := settingsWM(t)
		bg := m.bg
		open(m)
		if m.settings == nil && m.quitAsk == nil && m.palDlg == nil {
			t.Fatal("no dialog")
		}
		// Program output arrives while the dialog is up ...
		_, _ = bg.emu.Write([]byte("\x1b[1;1Hlive output while the dialog is up"))
		// ... and shows (the top line of the background isn't under the dialog).
		if screen := settingsScreen(m); !strings.Contains(strings.SplitN(screen, "\n", 2)[0], "live output") {
			t.Fatalf("output not shown:\n%s", screen)
		}
		// Typing goes to the dialog, not the window.
		before := len(bg.in.buf)
		typeText(m, "xz") // (no meaning in either dialog: "s" would save)
		if len(bg.in.buf) != before || (m.settings == nil && m.quitAsk == nil && m.palDlg == nil) {
			t.Error("keys reached the window, or closed the dialog")
		}
		// Clicks don't reach the windows either: the focus stays.
		f := m.focused
		m.handleMouse(uv.MouseClickEvent{X: 0, Y: 0, Button: uv.MouseLeft})
		if m.focused != f {
			t.Error("a click changed the focus")
		}
	}
}
