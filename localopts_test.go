package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseWindowSize(t *testing.T) {
	for in, want := range map[string]windowSize{
		"100x30":    {cols: 100, rows: 30},
		"100X30":    {cols: 100, rows: 30},
		" 80 x 24 ": {cols: 80, rows: 24},
		"60%x50%":   {colsPct: 0.6, rowsPct: 0.5},
		"100x50%":   {cols: 100, rowsPct: 0.5},
		"25%x100%":  {colsPct: 0.25, rowsPct: 1},
	} {
		if got, err := parseWindowSize(in); err != nil || got != want {
			t.Errorf("%q: %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "100", "0x10", "100x-1", "120%x50%", "0%x10", "ax b", "100x30x2"} {
		if _, err := parseWindowSize(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestReadLocalOpts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.toml")
	read := func(text string) (localOpts, []string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		lc, ok := readLocalConfig(path)
		if !ok {
			t.Fatalf("not read: %q", text)
		}
		return readLocalOpts(lc)
	}
	if o, w := read(`hostname = "x"`); o != defaultLocalOpts() || len(w) != 0 || !o.confirmQuit || o.exitOnLastWindow {
		t.Errorf("defaults: %+v %v", o, w)
	}
	o, w := read("new_window_size = \"90x25\"\nconfirm_quit = false\nexit_on_last_window = true\n")
	if len(w) != 0 || o.newSize != (windowSize{cols: 90, rows: 25}) || o.confirmQuit || !o.exitOnLastWindow {
		t.Errorf("set: %+v %v", o, w)
	}
	if o, w := read(`new_window_size = "huge"`); len(w) != 1 || !strings.Contains(w[0], "new_window_size") || o.newSize.set() {
		t.Errorf("bad size: %+v %v", o, w)
	}
}

func TestNewWindowSizeSetting(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 100, 41) // area 40 rows
	minH := 5
	if w, h := m.newWindowSize(minH, 40); w != 50 || h != 20 {
		t.Errorf("ratios: %dx%d", w, h)
	}
	m.local.newSize = windowSize{cols: 80, rows: 24}
	if w, h := m.newWindowSize(minH, 40); w != 80 || h != 24 {
		t.Errorf("80x24: %dx%d", w, h)
	}
	m.local.newSize = windowSize{colsPct: 0.7, rows: 100} // rows beyond the screen: clamped
	if w, h := m.newWindowSize(minH, 40); w != 70 || h != 40 {
		t.Errorf("70%%x100: %dx%d", w, h)
	}
	m.local.newSize = windowSize{cols: 2, rowsPct: 0.01} // below the minimums
	if w, h := m.newWindowSize(minH, 40); w != m.minW() || h != minH {
		t.Errorf("tiny: %dx%d", w, h)
	}
}

func TestConfirmQuitSetting(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	m.askQuit()
	if m.quit || m.quitAsk == nil {
		t.Fatal("default: should ask")
	}
	m.quitAsk = nil
	m.local.confirmQuit = false
	m.askQuit()
	if !m.quit || m.quitAsk != nil {
		t.Fatal("confirm_quit = false: should quit at once")
	}
}

func TestExitOnLastWindow(t *testing.T) {
	m, bg, a, b := wmWithBackground(t)
	m.toggleEvents() // doesn't count
	m.remove(a)
	m.remove(b)
	if m.quit {
		t.Fatal("quit with exit_on_last_window off")
	}

	m, bg, a, b = wmWithBackground(t)
	m.local.exitOnLastWindow = true
	m.toggleEvents()
	m.remove(a)
	if m.quit {
		t.Fatal("quit with a window left")
	}
	m.remove(b)
	if !m.quit {
		t.Fatal("last window closed: didn't quit (the events window must not count)")
	}
	_ = bg

	// Nothing to close at start: no quitting.
	m, _, a, b = wmWithBackground(t)
	m.local.exitOnLastWindow = true
	m.windows = []*Window{m.bg}
	m.remove(a) // not in the list any more
	if m.quit {
		t.Fatal("quit without having had a window")
	}
	_ = b
}
