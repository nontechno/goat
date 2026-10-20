package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// Keyboard protocols: how keys reach a program that asks for more than
// the classic encoding gives (where Shift+Enter is Enter, Ctrl+i is Tab,
// and Esc is the start of Alt+key).
//
// Kitty's keyboard protocol (https://sw.kovidgoyal.net/kitty/keyboard-protocol/):
// each screen (main, alternate) has a stack of flags.
//
//	CSI > flags u        push flags            CSI < n u   pop n (1)
//	CSI = flags ; m u    set (1), or (2), not (3)
//	CSI ? u              → CSI ? flags u
//
//	1  disambiguate: Esc, and keys with Ctrl/Alt/Super (and Enter, Tab,
//	   Backspace with any modifier) as CSI code ; modifiers u
//	   (Shift+Enter: CSI 13 ; 2 u)
//	2  event types: a held key's repeats as :2 (goat sees no releases)
//	4  alternate keys: the shifted key too (Ctrl+Shift+a: CSI 97:65 ; 6 u)
//	8  every key as an escape code, text keys included
//	16 the text a key types, with 8
//
// Claude Code pushes 5 (1 + 4) at start and pops at exit.
//
// xterm's modifyOtherKeys (CSI > 4 ; level m; CSI > 4 m resets; CSI ? 4 m
// asks): at level 2 keys with modifiers that have no code of their own are
// CSI 27 ; modifiers ; code ~ (Shift+Enter: CSI 27 ; 2 ; 13 ~).
//
// Kitty's protocol, when a program set flags, wins over modifyOtherKeys;
// with neither, keys are sent as always (input.go encodeKey).
//
// For goat to tell Shift+Enter from Enter at all, the terminal it runs in
// must report them apart: goat asks it for kitty flags 1 + 4 and for
// modifyOtherKeys 2 (screen.go writeModes; keyboard_enhancements in
// config.toml). Terminals that know neither (macOS Terminal) send keys as
// before, and Shift+Enter stays Enter.

// kittyStackMax is how deep a screen's flag stack goes (the oldest goes).
const kittyStackMax = 16

// keyboardState is a window's keyboard modes, as its program set them.
type keyboardState struct {
	stacks  [2][]int // kitty flags: main screen, alternate screen
	modKeys int      // modifyOtherKeys level (0, 1, 2)
	// owner is the foreground process group that last set main-screen
	// modes (0: unknown). Once another one is in front, other than the
	// shell's, those modes are dropped (dropOrphanKeyboard).
	owner int
}

// noteKeyboardOwner remembers who set main-screen keyboard modes.
func (w *Window) noteKeyboardOwner() {
	if w.screenIndex() == 0 && w.cmd != nil {
		w.kbd.owner = foregroundPid(w.ptmx, w.cmd)
	}
}

// dropOrphanKeyboard forgets the main screen's keyboard modes when the
// program that set them is no longer in front: it was killed, or quit,
// without undoing them, and the shell would get Ctrl+C as CSI 99;5u. The
// shell's own modes (fish sets them) are kept. Reports whether it did.
func (w *Window) dropOrphanKeyboard() bool {
	k := &w.kbd
	if k.owner <= 0 || k.owner == w.pid() || (len(k.stacks[0]) == 0 && k.modKeys == 0) || w.closed || w.cmd == nil {
		return false
	}
	if fg := foregroundPid(w.ptmx, w.cmd); fg <= 0 || fg == k.owner {
		return false
	}
	logger.Info("keyboard modes dropped: the program that set them is gone", "window", w.id, "pgrp", k.owner)
	k.stacks[0], k.modKeys, k.owner = nil, 0, 0
	return true
}

// screenIndex is 1 on the alternate screen, else 0.
func (w *Window) screenIndex() int {
	if w.emu != nil && w.emu.IsAltScreen() {
		return 1
	}
	return 0
}

// kittyFlags are the current screen's kitty keyboard flags.
func (w *Window) kittyFlags() int {
	s := w.kbd.stacks[w.screenIndex()]
	if len(s) == 0 {
		return 0
	}
	return s[len(s)-1]
}

// resetKeyboard forgets the keyboard modes (a full reset).
func (w *Window) resetKeyboard() { w.kbd = keyboardState{} }

// installKeyboard registers the keyboard protocol sequences on the
// window's emulator.
func (w *Window) installKeyboard() {
	reply := func(s string) { _, _ = io.WriteString(w.emu.InputPipe(), s) }
	param := func(p ansi.Params, i, def int) int {
		n, _, _ := p.Param(i, def)
		return n
	}
	w.emu.RegisterCsiHandler(ansi.Command('>', 0, 'u'), func(p ansi.Params) bool { // push
		w.noteKeyboardOwner()
		s := &w.kbd.stacks[w.screenIndex()]
		*s = append(*s, param(p, 0, 0)&31)
		if len(*s) > kittyStackMax {
			*s = (*s)[1:]
		}
		return true
	})
	w.emu.RegisterCsiHandler(ansi.Command('<', 0, 'u'), func(p ansi.Params) bool { // pop
		s := &w.kbd.stacks[w.screenIndex()]
		*s = (*s)[:max(len(*s)-max(param(p, 0, 1), 1), 0)] // popping an empty stack: nothing
		return true
	})
	w.emu.RegisterCsiHandler(ansi.Command('=', 0, 'u'), func(p ansi.Params) bool { // set
		w.noteKeyboardOwner()
		s := &w.kbd.stacks[w.screenIndex()]
		if len(*s) == 0 {
			*s = append(*s, 0)
		}
		top, f := &(*s)[len(*s)-1], param(p, 0, 0)&31
		switch param(p, 1, 1) {
		case 1:
			*top = f
		case 2:
			*top |= f
		case 3:
			*top &^= f
		}
		return true
	})
	w.emu.RegisterCsiHandler(ansi.Command('?', 0, 'u'), func(ansi.Params) bool { // query
		reply(fmt.Sprintf("\x1b[?%du", w.kittyFlags()))
		return true
	})
	w.emu.RegisterCsiHandler(ansi.Command('>', 0, 'm'), func(p ansi.Params) bool { // XTMODKEYS
		if param(p, 0, 0) == 4 {
			w.noteKeyboardOwner()
			w.kbd.modKeys = min(param(p, 1, 0), 2)
		}
		return true
	})
	w.emu.RegisterCsiHandler(ansi.Command('?', 0, 'm'), func(p ansi.Params) bool { // XTQMODKEYS
		if param(p, 0, 0) != 4 {
			return false
		}
		reply(fmt.Sprintf("\x1b[>4;%dm", w.kbd.modKeys))
		return true
	})
}

// encodeKey is a key as the window's program asked for keys: kitty's
// protocol, modifyOtherKeys, or the classic encoding.
func (w *Window) encodeKey(k uv.Key) []byte {
	if f := w.kittyFlags(); f != 0 {
		return encodeKitty(k, f, w.appCursor)
	}
	if w.kbd.modKeys > 0 {
		return encodeModifyOtherKeys(k, w.kbd.modKeys, w.appCursor)
	}
	return encodeKey(k, w.appCursor)
}

// kittyMods is the modifier field: 1 + shift 1, alt 2, ctrl 4, super 8,
// hyper 16, meta 32, caps lock 64, num lock 128 (the locks only with
// flag 8, as the spec has it).
func kittyMods(m uv.KeyMod, locks bool) int {
	n := 0
	for _, b := range []struct {
		mod uv.KeyMod
		bit int
	}{{uv.ModShift, 1}, {uv.ModAlt, 2}, {uv.ModCtrl, 4}, {uv.ModSuper, 8},
		{uv.ModHyper, 16}, {uv.ModMeta, 32}, {uv.ModCapsLock, 64}, {uv.ModNumLock, 128}} {
		if m.Contains(b.mod) && (locks || b.bit < 64) {
			n += b.bit
		}
	}
	return 1 + n
}

// kittyFunctional are keys with codes of their own: CSI number ~ or
// CSI 1 letter (with modifiers; unmodified CSI letter, or the classic form
// below flag 8).
var kittyFunctional = map[rune]struct {
	num    int
	letter byte // 0: CSI num ~
}{
	uv.KeyUp: {1, 'A'}, uv.KeyDown: {1, 'B'}, uv.KeyRight: {1, 'C'}, uv.KeyLeft: {1, 'D'},
	uv.KeyHome: {1, 'H'}, uv.KeyEnd: {1, 'F'}, uv.KeyInsert: {2, 0}, uv.KeyDelete: {3, 0},
	uv.KeyPgUp: {5, 0}, uv.KeyPgDown: {6, 0},
	uv.KeyF1: {1, 'P'}, uv.KeyF2: {1, 'Q'}, uv.KeyF3: {13, 0}, uv.KeyF4: {1, 'S'},
	uv.KeyF5: {15, 0}, uv.KeyF6: {17, 0}, uv.KeyF7: {18, 0}, uv.KeyF8: {19, 0},
	uv.KeyF9: {20, 0}, uv.KeyF10: {21, 0}, uv.KeyF11: {23, 0}, uv.KeyF12: {24, 0},
}

// kittyCodes are keys sent as CSI code u: their code.
var kittyCodes = map[rune]int{
	uv.KeyEscape: 27, uv.KeyEnter: 13, uv.KeyKpEnter: 13, uv.KeyTab: 9, uv.KeyBackspace: 127,
}

// encodeKitty is a key in kitty's keyboard protocol, for flags.
func encodeKitty(k uv.Key, flags int, appCursor bool) []byte {
	all := flags&8 != 0
	mods := kittyMods(k.Mod, all)
	plainMods := kittyMods(k.Mod, false) // without the locks
	event := ""
	if flags&2 != 0 && k.IsRepeat {
		event = ":2"
	}
	field := func(text string) string { // ";mods[:event][;text]"
		if mods == 1 && event == "" && text == "" {
			return ""
		}
		s := ";" + strconv.Itoa(mods) + event
		if text != "" {
			s += ";" + text
		}
		return s
	}

	// Arrows, Home, End, Insert, Delete, PageUp/Down, F1-F12.
	if fk, ok := kittyFunctional[k.Code]; ok {
		if !all && plainMods == 1 && event == "" {
			return encodeKey(k, appCursor) // the classic form (SS3 in cursor-key mode)
		}
		if fk.letter != 0 {
			if mods == 1 && event == "" {
				return []byte("\x1b[" + string(fk.letter))
			}
			return []byte(fmt.Sprintf("\x1b[1%s%c", field(""), fk.letter))
		}
		return []byte(fmt.Sprintf("\x1b[%d%s~", fk.num, field("")))
	}

	// Esc, Enter, Tab, Backspace.
	if code, ok := kittyCodes[k.Code]; ok {
		if k.Code == uv.KeyEscape || all || plainMods > 1 || event != "" {
			return []byte(fmt.Sprintf("\x1b[%d%su", code, field("")))
		}
		return encodeKey(k, appCursor)
	}

	// Text keys (Space included).
	code := k.Code
	if code == uv.KeySpace {
		code = ' '
	}
	if code < 0x20 || code >= uv.KeyExtended || code == 0x7f {
		return encodeKey(k, appCursor) // anything else: as always
	}
	shifted := k.ShiftedCode
	if code >= 'A' && code <= 'Z' { // an upper case code: the key is the lower case one, shifted
		shifted, code = code, code-'A'+'a'
		mods = kittyMods(k.Mod|uv.ModShift, all)
		plainMods = kittyMods(k.Mod|uv.ModShift, false)
	}
	textOnly := plainMods == 1 || plainMods == 2 // nothing but Shift: it types
	if !all && textOnly && event == "" {
		return encodeKey(k, appCursor)
	}
	key := strconv.Itoa(int(code))
	if shifted == 0 && k.Mod.Contains(uv.ModShift) && code >= 'a' && code <= 'z' {
		shifted = code - 'a' + 'A'
	}
	if flags&4 != 0 && k.Mod.Contains(uv.ModShift) && shifted != 0 && shifted != code {
		key += ":" + strconv.Itoa(int(shifted)) // alternate keys: the shifted one
	}
	text := ""
	if all && flags&16 != 0 && textOnly {
		t := k.Text
		if t == "" {
			t = string(code)
			if k.Mod.Contains(uv.ModShift) && shifted != 0 {
				t = string(shifted)
			}
		}
		var cps []string
		for _, r := range t {
			cps = append(cps, strconv.Itoa(int(r)))
		}
		text = strings.Join(cps, ":")
	}
	return []byte("\x1b[" + key + field(text) + "u")
}

// encodeModifyOtherKeys is a key under xterm's modifyOtherKeys: at level
// 2 every key with modifiers but Shift alone on a text key, and Enter,
// Tab, Backspace and Esc with any modifier, as CSI 27 ; mods ; code ~
// (keys with codes of their own, like arrows, as always); at level 1 only
// the keys the classic encoding can't send.
func encodeModifyOtherKeys(k uv.Key, level int, appCursor bool) []byte {
	if _, ok := kittyFunctional[k.Code]; ok {
		return encodeKey(k, appCursor)
	}
	mods := kittyMods(k.Mod, false) // the same numbering: 1 + shift 1, alt 2, ctrl 4, …
	if mods == 1 {
		return encodeKey(k, appCursor)
	}
	code := k.Code
	switch k.Code {
	case uv.KeyEnter, uv.KeyKpEnter:
		code = 13
	case uv.KeyTab:
		code = 9
	case uv.KeyBackspace:
		code = 127
	case uv.KeyEscape:
		code = 27
	case uv.KeySpace:
		code = ' '
	default:
		if code < 0x20 || code >= uv.KeyExtended {
			return encodeKey(k, appCursor)
		}
		if mods == 2 { // Shift alone on a text key: it types the shifted character
			return encodeKey(k, appCursor)
		}
		if k.Mod.Contains(uv.ModShift) && code >= 'a' && code <= 'z' {
			code = code - 'a' + 'A' // xterm reports the shifted character
		}
	}
	if classic := encodeKey(k, appCursor); level < 2 && classic != nil {
		return classic
	}
	return []byte(fmt.Sprintf("\x1b[27;%d;%d~", mods, code))
}
