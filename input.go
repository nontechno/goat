package main

import (
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"
)

// encodeKey turns a decoded key press into the bytes an xterm-compatible
// terminal sends for it. appCursor is the child's DECCKM state: when set,
// unmodified arrows and Home/End use SS3 (ESC O A) instead of CSI (ESC [ A).
//
// vt.Emulator.SendKey is not used because it drops every key that has a
// modifier outside its fixed table (Shift+letters, Ctrl+arrows, ...).
func encodeKey(k uv.Key, appCursor bool) []byte {
	mod := k.Mod
	alt := mod.Contains(uv.ModAlt)
	ctrl := mod.Contains(uv.ModCtrl)
	shift := mod.Contains(uv.ModShift)

	// xterm modifier parameter: 1 + shift + 2*alt + 4*ctrl.
	m := 1
	if shift {
		m++
	}
	if alt {
		m += 2
	}
	if ctrl {
		m += 4
	}
	letter := func(ch byte, ss3 bool) []byte {
		switch {
		case m > 1:
			return fmt.Appendf(nil, "\x1b[1;%d%c", m, ch)
		case ss3:
			return []byte{0x1b, 'O', ch}
		default:
			return []byte{0x1b, '[', ch}
		}
	}
	tilde := func(n int) []byte {
		if m > 1 {
			return fmt.Appendf(nil, "\x1b[%d;%d~", n, m)
		}
		return fmt.Appendf(nil, "\x1b[%d~", n)
	}
	// Keys that are a plain byte, with Alt sent as an ESC prefix.
	plain := func(b ...byte) []byte {
		if alt {
			return append([]byte{0x1b}, b...)
		}
		return b
	}

	switch k.Code {
	case uv.KeyUp:
		return letter('A', appCursor)
	case uv.KeyDown:
		return letter('B', appCursor)
	case uv.KeyRight:
		return letter('C', appCursor)
	case uv.KeyLeft:
		return letter('D', appCursor)
	case uv.KeyHome:
		return letter('H', appCursor)
	case uv.KeyEnd:
		return letter('F', appCursor)
	case uv.KeyInsert:
		return tilde(2)
	case uv.KeyDelete:
		return tilde(3)
	case uv.KeyPgUp:
		return tilde(5)
	case uv.KeyPgDown:
		return tilde(6)
	case uv.KeyF1, uv.KeyF2, uv.KeyF3, uv.KeyF4:
		return letter(byte('P'+(k.Code-uv.KeyF1)), true)
	case uv.KeyF5:
		return tilde(15)
	case uv.KeyF6:
		return tilde(17)
	case uv.KeyF7:
		return tilde(18)
	case uv.KeyF8:
		return tilde(19)
	case uv.KeyF9:
		return tilde(20)
	case uv.KeyF10:
		return tilde(21)
	case uv.KeyF11:
		return tilde(23)
	case uv.KeyF12:
		return tilde(24)
	case uv.KeyEnter, uv.KeyKpEnter:
		return plain('\r')
	case uv.KeyTab:
		if shift {
			return []byte("\x1b[Z")
		}
		return plain('\t')
	case uv.KeyBackspace:
		if ctrl {
			return plain(0x08)
		}
		return plain(0x7f)
	case uv.KeyEscape:
		return plain(0x1b)
	case uv.KeySpace:
		if ctrl {
			return plain(0x00)
		}
		return plain(' ')
	}

	if ctrl {
		if b, ok := ctrlByte(k.Code); ok {
			return plain(b)
		}
		return nil
	}
	if k.Text != "" {
		return plain([]byte(k.Text)...)
	}
	if k.Code >= 0x20 && k.Code < uv.KeyExtended && k.Code != 0x7f {
		s := string(k.Code)
		if shift && k.Code >= 'a' && k.Code <= 'z' {
			s = string(k.Code - 'a' + 'A')
		}
		return plain([]byte(s)...)
	}
	return nil
}

// ctrlByte is the control byte a terminal sends for Ctrl+r.
func ctrlByte(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(r-'a') + 1, true
	case r >= 'A' && r <= 'Z':
		return byte(r-'A') + 1, true
	}
	switch r {
	case ' ', '@', '2':
		return 0x00, true
	case '[', '3':
		return 0x1b, true
	case '\\', '4':
		return 0x1c, true
	case ']', '5':
		return 0x1d, true
	case '^', '6':
		return 0x1e, true
	case '_', '/', '7':
		return 0x1f, true
	case '?', '8':
		return 0x7f, true
	}
	return 0, false
}

// bindingRune is the character a key press stands for when matched against
// the Alt key bindings: Alt+Shift+h -> 'H', Alt+h -> 'h'. It returns false
// for keys that are not plain characters (arrows, Ctrl combinations, ...).
func bindingRune(k uv.Key) (rune, bool) {
	if k.Mod.Contains(uv.ModCtrl) || k.Code >= uv.KeyExtended || k.Code < 0x20 {
		return 0, false
	}
	r := k.Code
	if k.Mod.Contains(uv.ModShift) && r >= 'a' && r <= 'z' {
		r = r - 'a' + 'A'
	}
	return r, true
}

// macOptionChars maps what macOS Terminal and iTerm2 type for Option+key and
// Option+Shift+key (US layout, "Use Option as Meta key" off) back to the key.
// Option+e, i, n, u and ` are dead keys there: they type nothing until the
// next key, so they can't be recovered.
var macOptionChars = map[rune]rune{
	'å': 'a', '∫': 'b', 'ç': 'c', '∂': 'd', 'ƒ': 'f', '©': 'g', '˙': 'h',
	'∆': 'j', '˚': 'k', '¬': 'l', 'µ': 'm', 'ø': 'o', 'π': 'p', 'œ': 'q',
	'®': 'r', 'ß': 's', '†': 't', '√': 'v', '∑': 'w', '≈': 'x', '¥': 'y',
	'Ω': 'z',
	'Å': 'A', 'ı': 'B', 'Ç': 'C', 'Î': 'D', '´': 'E', 'Ï': 'F', '˝': 'G',
	'Ó': 'H', 'ˆ': 'I', 'Ô': 'J', '\uf8ff': 'K', 'Ò': 'L', 'Â': 'M',
	'˜': 'N', 'Ø': 'O', '∏': 'P', 'Œ': 'Q', '‰': 'R', 'Í': 'S', 'ˇ': 'T',
	'¨': 'U', '◊': 'V', '„': 'W', '˛': 'X', 'Á': 'Y', '¸': 'Z',
	'¡': '1', '™': '2', '£': '3', '¢': '4', '∞': '5', '§': '6', '¶': '7',
	'•': '8', 'ª': '9', 'º': '0',
	'¯': '<', '˘': '>', // Option+Shift+, and Option+Shift+.
}

// macOptionKey turns a key that is a macOS Option character into the
// Alt+key it stands for.
func macOptionKey(k uv.Key) (uv.Key, bool) {
	if k.Mod&^uv.ModShift != 0 {
		return k, false
	}
	r := k.Code
	if rs := []rune(k.Text); len(rs) == 1 {
		r = rs[0]
	}
	base, ok := macOptionChars[r]
	if !ok {
		return k, false
	}
	alt := uv.Key{Code: base, Text: string(base), Mod: uv.ModAlt}
	if base >= 'A' && base <= 'Z' {
		alt.Code, alt.Mod = base-'A'+'a', uv.ModAlt|uv.ModShift
	}
	return alt, true
}

// macOptionOn resolves the mac_option_keys setting.
//
// "auto" looks at the OS and at TERM_PROGRAM (set by Terminal.app and
// iTerm2 locally, but not passed on by ssh) and LC_TERMINAL (set by iTerm2,
// and passed on by a default ssh setup, which forwards LC_*).
func macOptionOn(setting, goos, termProgram, lcTerminal string) bool {
	switch setting {
	case "on":
		return true
	case "off":
		return false
	}
	return goos == "darwin" || termProgram == "Apple_Terminal" ||
		termProgram == "iTerm.app" || lcTerminal == "iTerm2"
}
