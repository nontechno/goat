package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"unicode/utf8"

	"github.com/charmbracelet/x/term"
)

// keyTest is "goat -keys": show what the terminal sends for each key press
// and what goat makes of it, to diagnose shortcuts that don't work (e.g. a
// macOS terminal typing ç for Option+c).
func keyTest(cfg *Config, cfgPath string, warns []string, in *os.File, out io.Writer) error {
	macOn := macOptionOn(cfg.MacOptionKeys, runtime.GOOS, os.Getenv("TERM_PROGRAM"), os.Getenv("LC_TERMINAL"))
	if cfgPath == "" {
		cfgPath = "(none found; using defaults)"
	}
	fmt.Fprintf(out, "goat %s key test\n", version)
	fmt.Fprintf(out, "  config file:      %s\n", cfgPath)
	for _, w := range warns {
		fmt.Fprintf(out, "  config warning:   %s\n", w)
	}
	fmt.Fprintf(out, "  mac_option_keys:  %q -> %v (os=%s TERM_PROGRAM=%q LC_TERMINAL=%q)\n",
		cfg.MacOptionKeys, onOff(macOn), runtime.GOOS, os.Getenv("TERM_PROGRAM"), os.Getenv("LC_TERMINAL"))
	fmt.Fprintf(out, "Press keys (e.g. Option/Alt+c). Ctrl+C ends.\n\n")

	state, err := term.MakeRaw(in.Fd())
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	defer func() { _ = term.Restore(in.Fd(), state) }()

	buf := make([]byte, 64)
	for {
		n, err := in.Read(buf)
		if err != nil {
			return nil
		}
		b := buf[:n]
		if n == 1 && b[0] == 0x03 {
			fmt.Fprint(out, "\r\n")
			return nil
		}
		fmt.Fprintf(out, "% x  %-10q -> %s\r\n", b, string(b), describeKeyBytes(cfg, b, macOn))
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// describeKeyBytes says how goat reads one read's worth of key bytes.
func describeKeyBytes(cfg *Config, b []byte, macOn bool) string {
	shortcut := func(r rune) string {
		if r >= '0' && r <= '9' {
			return "shortcut (focus window)"
		}
		if a, ok := cfg.bindings[r]; ok {
			return "shortcut: " + actionNames[a]
		}
		return "not a shortcut: goes to the program"
	}
	switch {
	case len(b) == 2 && b[0] == 0x1b && b[1] >= 0x20 && b[1] < 0x7f:
		return fmt.Sprintf("Alt+%c, %s", b[1], shortcut(rune(b[1])))
	case len(b) >= 3 && b[0] == 0x1b && (b[1] == '[' || b[1] == 'O' || b[1] == 0x1b):
		if name, ok := scrollKeyNames[string(b)]; ok {
			return name
		}
		return "escape sequence (arrow, function key, ...)"
	}
	r, size := utf8.DecodeRune(b)
	if size != len(b) || r == utf8.RuneError {
		return "several keys or unknown bytes"
	}
	if base, ok := macOptionChars[r]; ok {
		if !macOn {
			return fmt.Sprintf("macOS Option+%c character, but mac_option_keys is off: typed as text", base)
		}
		return fmt.Sprintf("macOS Option+%c, taken as Alt+%c, %s", base, base, shortcut(base))
	}
	if r < 0x20 {
		return "control key"
	}
	return "plain character"
}

// scrollKeyNames are the common encodings of goat's history keys (xterm
// modifier form, and Alt sent as an Esc prefix).
var scrollKeyNames = map[string]string{
	"\x1b[5;3~":   "Alt+PageUp, shortcut: scroll back a page",
	"\x1b[6;3~":   "Alt+PageDown, shortcut: scroll forward a page",
	"\x1b[5;4~":   "Alt+Shift+PageUp, shortcut: scroll back 3 lines",
	"\x1b[6;4~":   "Alt+Shift+PageDown, shortcut: scroll forward 3 lines",
	"\x1b[1;3H":   "Alt+Home, shortcut: oldest line",
	"\x1b[1;3F":   "Alt+End, shortcut: back to the live view",
	"\x1b\x1b[5~": "Alt+PageUp (Esc prefix), shortcut: scroll back a page",
	"\x1b\x1b[6~": "Alt+PageDown (Esc prefix), shortcut: scroll forward a page",
	"\x1b\x1b[H":  "Alt+Home (Esc prefix), shortcut: oldest line",
	"\x1b\x1b[F":  "Alt+End (Esc prefix), shortcut: back to the live view",
	"\x1b[5~":     "PageUp (no Alt: goes to the program)",
	"\x1b[6~":     "PageDown (no Alt: goes to the program)",
}

var actionNames = map[action]string{}

func init() {
	for a, n := range map[action]string{
		actNewWindow: "new window", actFocusNext: "focus next", actFocusPrev: "focus previous",
		actQuit: "quit", actCloseWindow: "close window", actPinWindow: "pin window",
		actCycleBackground: "next colors", actToggleWrap: "wrap / no wrap",
		actScrollLeft: "view left", actScrollRight: "view right", actShowEvents: "events window",
		actMoveLeft: "move left", actMoveDown: "move down", actMoveUp: "move up", actMoveRight: "move right",
		actResizeLeft: "narrower", actResizeDown: "taller", actResizeUp: "shorter", actResizeRight: "wider",
	} {
		actionNames[a] = n
	}
}
