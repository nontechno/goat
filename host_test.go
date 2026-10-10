package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHostLabel(t *testing.T) {
	sys, _ := os.Hostname()
	sys = cleanHostName(sys)
	for _, tt := range []struct {
		name     string
		file     *string // nil = no local.toml
		want     string
		wantFrom string
	}{
		{"no file", nil, sys, "system"},
		{"hostname set, trimmed", ptr("hostname = \"  my-box  \"\nhotkey = \"\"\n"), "my-box", "local.toml"},
		{"CRLF file", ptr("# local\r\nhostname = \"win-box\"\r\n"), "win-box", "local.toml"},
		{"cut to 32", ptr("hostname = \"" + strings.Repeat("x", 40) + "\"\n"), strings.Repeat("x", 32), "local.toml"},
		{"control chars dropped", ptr("hostname = \"a\\u001b[31mb\\u0007\"\n"), "a[31mb", "local.toml"},
		{"unicode kept", ptr("hostname = \"домашний-ПК 🐐\"\n"), "домашний-ПК 🐐", "local.toml"},
		{"blank name", ptr("hostname = \"   \"\n"), sys, "system"},
		{"no hostname key", ptr("hotkey = \"\"\n"), sys, "system"},
		{"not TOML", ptr("hostname: box\n"), sys, "system"},
		{"empty file", ptr(""), sys, "system"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "local.toml")
			if tt.file != nil {
				if err := os.WriteFile(path, []byte(*tt.file), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, from := hostLabel(path)
			if got != tt.want || from != tt.wantFrom {
				t.Errorf("got %q (%s), want %q (%s)", got, from, tt.want, tt.wantFrom)
			}
		})
	}
	// Unreadable local.toml (a directory here) falls back too.
	dir := filepath.Join(t.TempDir(), "local.toml")
	_ = os.Mkdir(dir, 0o755)
	if got, from := hostLabel(dir); got != sys || from != "system" {
		t.Errorf("directory as local.toml: %q (%s)", got, from)
	}
	// A long name is cut at 32 characters, not bytes.
	if got := cleanHostName(strings.Repeat("é", 40)); len([]rune(got)) != 32 {
		t.Errorf("cut by bytes: %q", got)
	}
	// ~/.hostname is no longer read.
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, ".hostname"), []byte("old-way\n"), 0o644)
	if got, _ := hostLabel(filepath.Join(home, "missing.toml")); got == "old-way" {
		t.Error("~/.hostname still read")
	}
}

// local.toml lives in goat's config directory.
func TestLocalConfigPath(t *testing.T) {
	x := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	if p := localConfigPath(); p != filepath.Join(x, "goat", "local.toml") {
		t.Fatalf("%q", p)
	}
}

func ptr(s string) *string { return &s }

func TestShowHostConfig(t *testing.T) {
	if !DefaultConfig().ShowHost {
		t.Fatal("show_host should default to on")
	}
	c, warns := parseConfig("show_host = false\n")
	if len(warns) != 0 || c.ShowHost {
		t.Fatalf("show_host = false: %v %v", c.ShowHost, warns)
	}
}

func TestDrawHost(t *testing.T) {
	bottom := func(frame frameStyle, width int, host string, show bool) string {
		cfg := DefaultConfig()
		cfg.ShowHost = show
		m := newWM(cfg, nil, width+2, 6)
		m.host = host
		w := fakeFramed(1, 0, 0, width, 5, true, frame)
		m.windows, m.focused = []*Window{w}, w
		return render(m)[4]
	}
	for _, tt := range []struct {
		name  string
		frame frameStyle
		width int
		host  string
		show  bool
		want  string
	}{
		{"full", frameFull, 30, "box", true, "└─ box ─────────────── wrap ─┘  "},
		{"compact", frameCompact, 30, "box", true, "└─ box ─────────────── wrap ─┘  "},
		{"off", frameFull, 30, "box", false, "└───────────────────── wrap ─┘  "},
		{"no name", frameFull, 30, "", true, "└───────────────────── wrap ─┘  "},
		{"shortened to fit", frameFull, 20, "averyveryverylonghostname", true, "└─ averyve ─ wrap ─┘  "},
		{"short name, narrow window", frameFull, 16, "box", true, "└─ box ─ wrap ─┘  "},
		{"too narrow: left out", frameFull, 14, "box", true, "└───── wrap ─┘  "},
		{"cut below 4 columns: left out", frameFull, 16, "longname", true, "└─────── wrap ─┘  "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := bottom(tt.frame, tt.width, tt.host, tt.show); got != tt.want {
				t.Errorf("bottom border\n got %q\nwant %q", got, tt.want)
			}
		})
	}
	// Frame "none" has no bottom border: nothing is drawn.
	cfg := DefaultConfig()
	m := newWM(cfg, nil, 22, 6)
	m.host = "box"
	w := fakeFramed(1, 0, 0, 20, 5, true, frameNone)
	m.windows, m.focused = []*Window{w}, w
	for i, row := range render(m) {
		if strings.Contains(row, "box") {
			t.Errorf("frame none: host drawn in row %d: %q", i, row)
		}
	}
}

// A FIFO (or other special file) at local.toml is skipped without
// blocking: it is read on the event loop.
func TestHostFileNotRegular(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.toml")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	done := make(chan string, 1)
	go func() { n, _ := hostLabel(path); done <- n }()
	select {
	case n := <-done:
		if want := cleanHostName(must(os.Hostname())); n != want {
			t.Errorf("got %q, want the system name %q", n, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading a FIFO at local.toml blocked")
	}
}

func must[T any](v T, _ error) T { return v }
