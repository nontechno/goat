package main

import (
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestEncodeKitty(t *testing.T) {
	k := func(code rune, mod uv.KeyMod) uv.Key { return uv.Key{Code: code, Mod: mod} }
	for _, tc := range []struct {
		name  string
		key   uv.Key
		flags int
		want  string
	}{
		// Flag 1, disambiguate (Claude Code pushes 5 = 1 + 4).
		{"Shift+Enter", k(uv.KeyEnter, uv.ModShift), 5, "\x1b[13;2u"},
		{"Enter", k(uv.KeyEnter, 0), 5, "\r"},
		{"Ctrl+Enter", k(uv.KeyEnter, uv.ModCtrl), 5, "\x1b[13;5u"},
		{"Esc", k(uv.KeyEscape, 0), 5, "\x1b[27u"},
		{"Alt+Esc", k(uv.KeyEscape, uv.ModAlt), 5, "\x1b[27;3u"},
		{"Tab", k(uv.KeyTab, 0), 5, "\t"},
		{"Shift+Tab", k(uv.KeyTab, uv.ModShift), 5, "\x1b[9;2u"},
		{"Backspace", k(uv.KeyBackspace, 0), 5, "\x7f"},
		{"Ctrl+Backspace", k(uv.KeyBackspace, uv.ModCtrl), 5, "\x1b[127;5u"},
		{"a", uv.Key{Code: 'a', Text: "a"}, 5, "a"},
		{"Shift+a", uv.Key{Code: 'a', Mod: uv.ModShift, Text: "A"}, 5, "A"},
		{"Ctrl+a", k('a', uv.ModCtrl), 5, "\x1b[97;5u"},
		{"Ctrl+i", k('i', uv.ModCtrl), 1, "\x1b[105;5u"},
		{"Alt+a", k('a', uv.ModAlt), 5, "\x1b[97;3u"},
		{"Ctrl+Shift+a, alternate keys", k('a', uv.ModCtrl|uv.ModShift), 5, "\x1b[97:65;6u"},
		{"Ctrl+Shift+a, no alternate", k('a', uv.ModCtrl|uv.ModShift), 1, "\x1b[97;6u"},
		{"Ctrl+Space", k(uv.KeySpace, uv.ModCtrl), 1, "\x1b[32;5u"},
		{"Up", k(uv.KeyUp, 0), 1, "\x1b[A"},
		{"Ctrl+Up", k(uv.KeyUp, uv.ModCtrl), 1, "\x1b[1;5A"},
		{"F3+Shift", k(uv.KeyF3, uv.ModShift), 1, "\x1b[13;2~"},
		{"F1", k(uv.KeyF1, 0), 1, "\x1bOP"},
		{"PageUp+Alt", k(uv.KeyPgUp, uv.ModAlt), 1, "\x1b[5;3~"},
		// Flag 8: every key; 16: the text too.
		{"a, all keys", uv.Key{Code: 'a', Text: "a"}, 9, "\x1b[97u"},
		{"Enter, all keys", k(uv.KeyEnter, 0), 9, "\x1b[13u"},
		{"Up, all keys", k(uv.KeyUp, 0), 9, "\x1b[A"},
		{"a, all keys with text", uv.Key{Code: 'a', Text: "a"}, 25, "\x1b[97;1;97u"},
		{"Shift+a, all keys, alt, text", uv.Key{Code: 'a', Mod: uv.ModShift, Text: "A"}, 29, "\x1b[97:65;2;65u"},
		// Flag 2: a repeat.
		{"Ctrl+a repeat", uv.Key{Code: 'a', Mod: uv.ModCtrl, IsRepeat: true}, 3, "\x1b[97;5:2u"},
	} {
		if got := string(encodeKitty(tc.key, tc.flags, false)); got != tc.want {
			t.Errorf("%s (flags %d): %q, want %q", tc.name, tc.flags, got, tc.want)
		}
	}
}

func TestEncodeModifyOtherKeys(t *testing.T) {
	k := func(code rune, mod uv.KeyMod) uv.Key { return uv.Key{Code: code, Mod: mod} }
	for _, tc := range []struct {
		name  string
		key   uv.Key
		level int
		want  string
	}{
		{"Shift+Enter", k(uv.KeyEnter, uv.ModShift), 2, "\x1b[27;2;13~"},
		{"Ctrl+Enter", k(uv.KeyEnter, uv.ModCtrl), 2, "\x1b[27;5;13~"},
		{"Enter", k(uv.KeyEnter, 0), 2, "\r"},
		{"Ctrl+a", k('a', uv.ModCtrl), 2, "\x1b[27;5;97~"},
		{"Ctrl+Shift+a", k('a', uv.ModCtrl|uv.ModShift), 2, "\x1b[27;6;65~"},
		{"Shift+a types", uv.Key{Code: 'a', Mod: uv.ModShift, Text: "A"}, 2, "A"},
		{"Alt+x", k('x', uv.ModAlt), 2, "\x1b[27;3;120~"},
		{"Ctrl+Up as always", k(uv.KeyUp, uv.ModCtrl), 2, "\x1b[1;5A"},
		{"level 1: Ctrl+a classic", k('a', uv.ModCtrl), 1, "\x01"},
		{"level 1: Ctrl+1 has no classic code", k('1', uv.ModCtrl), 1, "\x1b[27;5;49~"},
	} {
		if got := string(encodeModifyOtherKeys(tc.key, tc.level, false)); got != tc.want {
			t.Errorf("%s (level %d): %q, want %q", tc.name, tc.level, got, tc.want)
		}
	}
}

// What Claude Code does (claude-server.md): push 5 at start, query, pop
// (more than pushed) at exit; modifyOtherKeys 2 then reset. Keys follow.
func TestKeyboardModes(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	w, replies := hookedWindow(t, m)
	shiftEnter := uv.Key{Code: uv.KeyEnter, Mod: uv.ModShift}
	if got := string(w.encodeKey(shiftEnter)); got != "\r" {
		t.Fatalf("before: %q", got)
	}
	_ = w.feed([]byte("\x1b[?u"))
	if got := replies(200 * time.Millisecond); got != "\x1b[?0u" {
		t.Fatalf("query: %q", got)
	}
	_ = w.feed([]byte("\x1b[<u\x1b[>5u\x1b[>4;2m\x1b[?u\x1b[?4m"))
	if got := replies(200 * time.Millisecond); got != "\x1b[?5u\x1b[>4;2m" {
		t.Fatalf("after push: %q", got)
	}
	if got := string(w.encodeKey(shiftEnter)); got != "\x1b[13;2u" {
		t.Fatalf("kitty: %q", got)
	}
	// The alternate screen has a stack of its own.
	_ = w.feed([]byte("\x1b[?1049h"))
	if got := string(w.encodeKey(shiftEnter)); got != "\x1b[27;2;13~" {
		t.Fatalf("alternate screen (modifyOtherKeys only): %q", got)
	}
	_ = w.feed([]byte("\x1b[?1049l"))
	// =flags;mode: or, and-not, set.
	_ = w.feed([]byte("\x1b[=8;2u"))
	if w.kittyFlags() != 13 {
		t.Fatalf("or: %d", w.kittyFlags())
	}
	_ = w.feed([]byte("\x1b[=4;3u"))
	if w.kittyFlags() != 9 {
		t.Fatalf("and-not: %d", w.kittyFlags())
	}
	// Claude Code's exit: pops more than it pushed, resets modifyOtherKeys.
	_ = w.feed([]byte("\x1b[<u\x1b[>4m\x1b[>4m\x1b[<u\x1b[<u"))
	if w.kittyFlags() != 0 || w.kbd.modKeys != 0 {
		t.Fatalf("after exit: flags %d, modKeys %d", w.kittyFlags(), w.kbd.modKeys)
	}
	if got := string(w.encodeKey(shiftEnter)); got != "\r" {
		t.Fatalf("after exit: %q", got)
	}
	// A full reset forgets everything.
	_ = w.feed([]byte("\x1b[>1u\x1bc"))
	if w.kittyFlags() != 0 {
		t.Fatal("kept after RIS")
	}
}

// goat's shortcuts still match when the terminal reports keys with kitty's
// protocol: Alt+Shift+, (with the shifted key) is Alt+<.
func TestBindingRuneShifted(t *testing.T) {
	if r, ok := bindingRune(uv.Key{Code: ',', ShiftedCode: '<', Mod: uv.ModAlt | uv.ModShift}); !ok || r != '<' {
		t.Errorf("Alt+Shift+,: %q", r)
	}
	if r, ok := bindingRune(uv.Key{Code: 'h', Mod: uv.ModAlt | uv.ModShift}); !ok || r != 'H' {
		t.Errorf("Alt+Shift+h: %q", r)
	}
}
