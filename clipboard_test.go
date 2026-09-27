package main

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func fakeClipEnv(vars map[string]string, tools []string, goos string, wsl bool) clipboardEnv {
	return clipboardEnv{
		getenv: func(k string) string { return vars[k] },
		lookPath: func(n string) (string, error) {
			if slices.Contains(tools, n) {
				return "/usr/bin/" + n, nil
			}
			return "", errors.New("not found")
		},
		goos: goos, wsl: wsl,
	}
}

func TestClipboardCommandDetection(t *testing.T) {
	all := []string{"tmux", "clip.exe", "pbcopy", "wl-copy", "xclip", "xsel"}
	cases := []struct {
		name string
		cfg  string
		env  clipboardEnv
		want string
	}{
		{"configured", "xsel -ib", fakeClipEnv(nil, nil, "linux", false), "xsel -ib"},
		{"none", "none", fakeClipEnv(map[string]string{"TMUX": "x"}, all, "linux", false), ""},
		{"tmux first", "", fakeClipEnv(map[string]string{"TMUX": "x", "DISPLAY": ":0"}, all, "linux", false), "tmux load-buffer -w -"},
		{"wsl", "", fakeClipEnv(nil, all, "linux", true), "clip.exe"},
		{"mac", "", fakeClipEnv(nil, all, "darwin", false), "pbcopy"},
		{"wayland", "", fakeClipEnv(map[string]string{"WAYLAND_DISPLAY": "w"}, all, "linux", false), "wl-copy"},
		{"x11 xclip", "", fakeClipEnv(map[string]string{"DISPLAY": ":0"}, all, "linux", false), "xclip -selection clipboard"},
		{"x11 xsel", "", fakeClipEnv(map[string]string{"DISPLAY": ":0"}, []string{"xsel"}, "linux", false), "xsel --clipboard --input"},
		{"ssh, nothing", "", fakeClipEnv(nil, all, "linux", false), ""},
		{"tmux var but no binary", "", fakeClipEnv(map[string]string{"TMUX": "x"}, nil, "linux", false), ""},
	}
	for _, c := range cases {
		if got := strings.Join(clipboardCommand(c.cfg, c.env), " "); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestClipboardCommandReportsFailure(t *testing.T) {
	got := make(chan string, 1)
	runClipboardCommand([]string{"sh", "-c", "echo boom >&2; exit 3"}, "x", func(m string) { got <- m })
	select {
	case m := <-got:
		if !strings.Contains(m, "copy with sh failed: boom") {
			t.Fatalf("message %q", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no failure reported")
	}
}

func TestClipboardViaTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	sock := t.TempDir() + "/tmux.sock"
	env := append(os.Environ(), "TMUX_TMPDIR="+t.TempDir())
	start := exec.Command("tmux", "-S", sock, "new-session", "-d", "-s", "t", "sleep 30")
	start.Env = env
	if err := start.Run(); err != nil {
		t.Skipf("cannot start tmux: %v", err)
	}
	defer exec.Command("tmux", "-S", sock, "kill-server").Run()

	failed := make(chan string, 1)
	// Same command gloat detects inside tmux, pointed at the test server.
	runClipboardCommand([]string{"tmux", "-S", sock, "load-buffer", "-"}, "copied via tmux", func(m string) { failed <- m })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("tmux", "-S", sock, "show-buffer").Output()
		if string(out) == "copied via tmux" {
			return
		}
		select {
		case m := <-failed:
			t.Fatal(m)
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("text never reached the tmux buffer")
}

func TestParseOSC52(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"52;c;" + enc([]byte("hello from mini")), "hello from mini", true},
		{"52;;" + enc([]byte("empty selection param")), "empty selection param", true},
		{"52;p;" + enc([]byte("primary")), "primary", true},
		{"52;c;" + enc([]byte("日本語 ✓")), "日本語 ✓", true},
		{"52;c;aGVsbG8", "hello", true},    // unpadded
		{"52;c;aGVs\nbG8=", "hello", true}, // wrapped
		{"52;c;?", "", false},              // read request: never forwarded
		{"52;c;", "", false},               // clear
		{"52;c;!!!not base64", "", false},  // garbage
		{"52;c", "", false},                // malformed
	}
	for _, c := range cases {
		got, ok := parseOSC52([]byte(c.in))
		if got != c.want || ok != c.ok {
			t.Errorf("%q: got %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestOSC52FromProgramIsForwarded(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 40, 10)
	w := fakeWindow(1, 0, 0, 20, 6, true, false)
	w.procName = "vim"
	m.windows, m.focused = []*Window{w}, w
	var gotText, gotWho string
	m.copy = func(text, who string) { gotText, gotWho = text, who }
	// Wire the handler the way newWindow does, then feed the program's bytes.
	w.emu.RegisterOscHandler(52, func(data []byte) bool { w.onClipboard(data); return true })
	m.hookClipboard(w)
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("yanked line")) + "\x07"
	_, _ = w.emu.WriteString(seq)
	if gotText != "yanked line" || gotWho != "vim" {
		t.Fatalf("got %q from %q", gotText, gotWho)
	}
	// A read request must not reach the clipboard.
	gotText = ""
	_, _ = w.emu.WriteString("\x1b]52;c;?\x07")
	if gotText != "" {
		t.Fatal("read request forwarded")
	}
	// ST-terminated form works too.
	_, _ = w.emu.WriteString("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("st form")) + "\x1b\\")
	if gotText != "st form" {
		t.Fatalf("ST form: %q", gotText)
	}
}
