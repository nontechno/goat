package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Copying selected text to the clipboard.
//
// Two independent routes, both used when available:
//
//   - OSC 52: goat asks the terminal to set its clipboard. Works over SSH,
//     but the terminal must support and allow it (PuTTY, GNOME Terminal and
//     other VTE terminals, and macOS Terminal.app don't; xterm and tmux need
//     it enabled).
//   - A clipboard command: copy_command from the config, or, when that is
//     empty, the first one that fits the environment (below).

// clipboardEnv is what auto-detection looks at (a struct so tests can fake it).
type clipboardEnv struct {
	getenv   func(string) string
	lookPath func(string) (string, error)
	goos     string
	wsl      bool
}

func realClipboardEnv() clipboardEnv {
	b, _ := os.ReadFile("/proc/version")
	return clipboardEnv{
		getenv:   os.Getenv,
		lookPath: exec.LookPath,
		goos:     runtime.GOOS,
		wsl:      os.Getenv("WSL_DISTRO_NAME") != "" || strings.Contains(strings.ToLower(string(b)), "microsoft"),
	}
}

// clipboardCommand returns the command to pipe copied text to, or nil.
// configured is copy_command: "" = detect, "none" = no command.
func clipboardCommand(configured string, e clipboardEnv) []string {
	switch configured = strings.TrimSpace(configured); configured {
	case "none":
		return nil
	case "":
	default:
		return strings.Fields(configured)
	}
	has := func(name string) bool { _, err := e.lookPath(name); return err == nil }
	switch {
	case e.getenv("TMUX") != "" && has("tmux"):
		// Into tmux's paste buffer; -w also sets the outer terminal's
		// clipboard (tmux 3.2+) when tmux knows it can.
		return []string{"tmux", "load-buffer", "-w", "-"}
	case e.wsl && has("clip.exe"):
		return []string{"clip.exe"}
	case e.goos == "darwin" && has("pbcopy"):
		return []string{"pbcopy"}
	case e.getenv("WAYLAND_DISPLAY") != "" && has("wl-copy"):
		return []string{"wl-copy"}
	case e.getenv("DISPLAY") != "" && has("xclip"):
		return []string{"xclip", "-selection", "clipboard"}
	case e.getenv("DISPLAY") != "" && has("xsel"):
		return []string{"xsel", "--clipboard", "--input"}
	}
	return nil
}

// runClipboardCommand pipes text to args. It runs in the background; a
// failure is reported through report (which must not block).
func runClipboardCommand(args []string, text string, report func(string)) {
	run := func(a []string) error {
		cmd := exec.Command(a[0], a[1:]...)
		cmd.Stdin = strings.NewReader(text)
		out, err := cmd.CombinedOutput()
		if err != nil && len(out) > 0 {
			err = fmt.Errorf("%s", strings.TrimSpace(string(out)))
		}
		return err
	}
	go func() {
		err := run(args)
		// tmux older than 3.2 has no -w: fall back to just its buffer.
		if err != nil && args[0] == "tmux" && len(args) == 4 && args[2] == "-w" {
			err = run([]string{"tmux", "load-buffer", "-"})
		}
		if err != nil {
			report(fmt.Sprintf("copy with %s failed: %v", args[0], err))
		}
	}()
}

// hookClipboard forwards OSC 52 clipboard writes from w's program to the
// clipboard (the host terminal and/or the clipboard command, like a mouse
// selection). Requests to read the clipboard ("?") are not forwarded, so
// programs can't read what's on it.
func (m *WM) hookClipboard(w *Window) {
	w.onClipboard = func(osc []byte) {
		text, ok := parseOSC52(osc)
		if !ok || m.copy == nil {
			return
		}
		m.copy(text, w.displayTitle(m.cfg.TitleSource))
	}
}

// parseOSC52 decodes "52;<selection>;<base64>" into the text to copy. It
// rejects queries ("?"), clears (empty) and malformed data.
func parseOSC52(osc []byte) (string, bool) {
	parts := strings.SplitN(string(osc), ";", 3)
	if len(parts) != 3 || parts[0] != "52" {
		return "", false
	}
	payload := strings.TrimSpace(parts[2])
	if payload == "" || payload == "?" {
		return "", false
	}
	// Some programs wrap long base64 (e.g. busybox base64 at 76 columns).
	payload = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' {
			return -1
		}
		return r
	}, payload)
	b, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		if b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(payload, "=")); err != nil {
			return "", false
		}
	}
	return string(b), true
}
