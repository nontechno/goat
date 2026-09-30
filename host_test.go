package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostLabel(t *testing.T) {
	sys, _ := os.Hostname()
	sys = cleanHostName(sys)
	for _, tt := range []struct {
		name     string
		file     *string // nil = no ~/.hostname
		want     string
		wantFrom string
	}{
		{"no file", nil, sys, "system"},
		{"first line, trimmed", ptr("  my-box  \nsecond line\n"), "my-box", "file"},
		{"CRLF", ptr("win-box\r\n"), "win-box", "file"},
		{"no newline", ptr("solo"), "solo", "file"},
		{"cut to 32", ptr(strings.Repeat("x", 40) + "\n"), strings.Repeat("x", 32), "file"},
		{"control chars dropped", ptr("a\x1b[31mb\x07\n"), "a[31mb", "file"},
		{"unicode kept", ptr("домашний-ПК 🐐\n"), "домашний-ПК 🐐", "file"},
		{"blank first line", ptr("   \nignored\n"), sys, "system"},
		{"empty file", ptr(""), sys, "system"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			if tt.file != nil {
				if err := os.WriteFile(filepath.Join(home, ".hostname"), []byte(*tt.file), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, from := hostLabel(home)
			if got != tt.want || from != tt.wantFrom {
				t.Errorf("got %q (%s), want %q (%s)", got, from, tt.want, tt.wantFrom)
			}
		})
	}
	// Unreadable ~/.hostname (a directory here) falls back too.
	home := t.TempDir()
	_ = os.Mkdir(filepath.Join(home, ".hostname"), 0o755)
	if got, from := hostLabel(home); got != sys || from != "system" {
		t.Errorf("directory as ~/.hostname: %q (%s)", got, from)
	}
	// A long first line is cut at 32 characters, not bytes.
	if got := cleanHostName(strings.Repeat("é", 40)); len([]rune(got)) != 32 {
		t.Errorf("cut by bytes: %q", got)
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
