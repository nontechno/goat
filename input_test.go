package main

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestEncodeKey(t *testing.T) {
	cases := []struct {
		name string
		key  uv.Key
		app  bool
		want string
	}{
		{"text", uv.Key{Code: 'a', Text: "a"}, false, "a"},
		{"shifted text", uv.Key{Code: 'a', Text: "A", Mod: uv.ModShift}, false, "A"},
		{"unicode", uv.Key{Code: 'é', Text: "é"}, false, "é"},
		{"ctrl+c", uv.Key{Code: 'c', Mod: uv.ModCtrl}, false, "\x03"},
		{"ctrl+space", uv.Key{Code: uv.KeySpace, Mod: uv.ModCtrl}, false, "\x00"},
		{"ctrl+backslash", uv.Key{Code: '\\', Mod: uv.ModCtrl}, false, "\x1c"},
		{"alt+b", uv.Key{Code: 'b', Mod: uv.ModAlt}, false, "\x1bb"},
		{"alt+ctrl+a", uv.Key{Code: 'a', Mod: uv.ModAlt | uv.ModCtrl}, false, "\x1b\x01"},
		{"enter", uv.Key{Code: uv.KeyEnter}, false, "\r"},
		{"backspace", uv.Key{Code: uv.KeyBackspace}, false, "\x7f"},
		{"shift+tab", uv.Key{Code: uv.KeyTab, Mod: uv.ModShift}, false, "\x1b[Z"},
		{"up", uv.Key{Code: uv.KeyUp}, false, "\x1b[A"},
		{"up app-cursor", uv.Key{Code: uv.KeyUp}, true, "\x1bOA"},
		{"home app-cursor", uv.Key{Code: uv.KeyHome}, true, "\x1bOH"},
		{"ctrl+left", uv.Key{Code: uv.KeyLeft, Mod: uv.ModCtrl}, true, "\x1b[1;5D"},
		{"shift+up", uv.Key{Code: uv.KeyUp, Mod: uv.ModShift}, false, "\x1b[1;2A"},
		{"delete", uv.Key{Code: uv.KeyDelete}, false, "\x1b[3~"},
		{"ctrl+pgdown", uv.Key{Code: uv.KeyPgDown, Mod: uv.ModCtrl}, false, "\x1b[6;5~"},
		{"f1", uv.Key{Code: uv.KeyF1}, false, "\x1bOP"},
		{"f5", uv.Key{Code: uv.KeyF5}, false, "\x1b[15~"},
		{"f12", uv.Key{Code: uv.KeyF12}, false, "\x1b[24~"},
		{"esc", uv.Key{Code: uv.KeyEscape}, false, "\x1b"},
	}
	for _, c := range cases {
		if got := string(encodeKey(c.key, c.app)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBindingRune(t *testing.T) {
	cases := []struct {
		key  uv.Key
		want rune
		ok   bool
	}{
		{uv.Key{Code: 'h', Mod: uv.ModAlt}, 'h', true},
		{uv.Key{Code: 'h', Mod: uv.ModAlt | uv.ModShift}, 'H', true},
		{uv.Key{Code: '3', Mod: uv.ModAlt}, '3', true},
		{uv.Key{Code: 'h', Mod: uv.ModCtrl | uv.ModAlt}, 0, false},
		{uv.Key{Code: uv.KeyUp, Mod: uv.ModAlt}, 0, false},
	}
	for _, c := range cases {
		got, ok := bindingRune(c.key)
		if got != c.want || ok != c.ok {
			t.Errorf("%v: got %q,%v want %q,%v", c.key, got, ok, c.want, c.ok)
		}
	}
}
