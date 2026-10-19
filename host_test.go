package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The host name is the system's: a "hostname" in local.toml (the old way)
// is ignored; aliases (alias.toml) change only what is shown.
func TestSystemHostName(t *testing.T) {
	sys := cleanHostName(must(os.Hostname()))
	if got := systemHostName(); got != sys {
		t.Errorf("got %q, want %q", got, sys)
	}
	path := filepath.Join(t.TempDir(), "local.toml")
	_ = os.WriteFile(path, []byte("hostname = \"friendly\"\n"), 0o644)
	if b := readBarRight(path); b.host != sys {
		t.Errorf("local.toml hostname still used: %q", b.host)
	}
	// A long name is cut at 32 characters, not bytes.
	if got := cleanHostName(strings.Repeat("é", 40)); len([]rune(got)) != 32 {
		t.Errorf("cut by bytes: %q", got)
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
		{"full", frameFull, 30, "box", true, "└─ 💻box ─────────────────[]─┘  "},
		{"compact", frameCompact, 30, "box", true, "└─ 💻box ─────────────────[]─┘  "},
		{"off", frameFull, 30, "box", false, "└─────────────────────────[]─┘  "},
		{"no name", frameFull, 30, "", true, "└─────────────────────────[]─┘  "},
		{"shortened to fit", frameFull, 19, "averyveryverylonghostname", true, "└─ 💻averyver ─[]─┘  "},
		{"short name, narrow window", frameFull, 14, "box", true, "└─ 💻box ─[]─┘  "},
		{"too narrow: left out", frameFull, 13, "box", true, "└────────[]─┘  "},
		{"symbol and 4 characters", frameFull, 15, "longname", true, "└─ 💻long ─[]─┘  "},
		{"cut below 4 characters: left out", frameFull, 14, "longname", true, "└─────────[]─┘  "},
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
	go func() { done <- readBarRight(path).host }()
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

// The frame shows 🧑user 💻host, or one of them; logs keep user@host.
func TestIdentityFrameText(t *testing.T) {
	id := identity{user: "alice", host: "box"}
	for _, tc := range []struct {
		user, host bool
		want       string
	}{
		{true, true, "🧑alice 💻box"},
		{false, true, "💻box"},
		{true, false, "🧑alice"},
		{false, false, ""},
	} {
		if got := id.frameText(tc.user, tc.host); got != tc.want {
			t.Errorf("user %v host %v: %q, want %q", tc.user, tc.host, got, tc.want)
		}
	}
	if got := (identity{host: "box"}).frameText(true, true); got != "💻box" {
		t.Errorf("no user known: %q", got)
	}
	if got := id.label(true, true); got != "alice@box" {
		t.Errorf("log label: %q", got)
	}
}

func TestBarRightSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local.toml")
	sys := systemHostName()
	for _, tc := range []struct {
		file string
		want barRight
	}{
		{"", barRight{clock: true, host: sys}},                      // no file: both, the system's name
		{`hostname = "friendly"`, barRight{clock: true, host: sys}}, // (no longer read: alias.toml)
		{"show_clock = false\nshow_launch_host = false\n", barRight{}},
		{"hostname = \"box\"\nshow_clock = false\n", barRight{host: sys}},
		{"show_launch_host = false\n", barRight{clock: true}},
		{"not toml [", barRight{clock: true, host: sys}},
	} {
		_ = os.Remove(path)
		if tc.file != "" {
			if err := os.WriteFile(path, []byte(tc.file), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := readBarRight(path); got != tc.want {
			t.Errorf("%q: %+v, want %+v", tc.file, got, tc.want)
		}
	}

	// Drawn: host name left of the clock; neither when both are off.
	m, _, _, _ := wmWithBackground(t)
	m.bar = barRight{clock: true, host: "friendly"}
	if line := strings.TrimRight(statusLine(m), " "); !strings.Contains(line, hostSymbol+"friendly  ") ||
		!strings.HasSuffix(line, time.Now().Format("15:04")) {
		t.Errorf("status bar %q", line)
	}
	m.bar = barRight{}
	if line := statusLine(m); strings.Contains(line, hostSymbol) || strings.Contains(line, time.Now().Format("15:04")) {
		t.Errorf("both off: %q", line)
	}
	// Edits to local.toml are picked up.
	if err := os.WriteFile(path, []byte("show_clock = false\nhostname = \"new\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.localPath, m.barChecked = path, time.Now().Add(-time.Minute)
	if !m.refreshBarRight(time.Now()) || m.bar != (barRight{host: systemHostName()}) {
		t.Errorf("not re-read: %+v", m.bar)
	}
}
