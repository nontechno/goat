package main

import (
	"fmt"
	"io"

	"github.com/charmbracelet/x/ansi"
)

// Replies to what programs ask the terminal, besides those the emulator
// answers itself (device attributes, cursor position, modes, colors) and
// OSC 7501 (status.go). Every reply goes through the emulator's reply
// stream as the question is read, so replies reach the program in the
// order it asked (claude-server.md, section 3).
//
//	CSI > 0 q      XTVERSION    DCS > | goat <version> ST
//	CSI 16 t       cell size    CSI 6 ; <height> ; <width> t  (pixels; when
//	                            goat's terminal told it, else no reply)
//	CSI 14 t       text area    CSI 4 ; <height> ; <width> t  (pixels; same)
//	CSI 18 t       text area    CSI 8 ; <rows> ; <columns> t
//	CSI ? 996 n    color scheme CSI ? 997 ; 1 n (dark) or 2 (light)
//
// Color scheme reports (mode 2031, as in Ghostty, kitty and Contour): while
// a program has the mode set, goat tells it CSI ? 997 ; 1 n or ; 2 n when
// it sets the mode and whenever the window's background turns from dark
// to light or back: a palette chosen with Alt+t, or goat's terminal
// changing its own colors. Dark or light is the window's palette's theme,
// else the tone of its background (wm.go backgroundTone).

// modeColorScheme is DEC private mode 2031: color scheme reports.
const modeColorScheme = ansi.DECMode(2031)

// installReplies registers the replies on a window's emulator. cell is the
// size of a character cell in pixels (0, 0 if not known).
func (w *Window) installReplies(cell func() (width, height int)) {
	reply := func(s string) { _, _ = io.WriteString(w.emu.InputPipe(), s) }
	w.emu.RegisterCsiHandler(ansi.Command('>', 0, 'q'), func(params ansi.Params) bool {
		if n, _, _ := params.Param(0, 0); n != 0 {
			return false
		}
		reply("\x1bP>|goat " + version + "\x1b\\")
		return true
	})
	w.emu.RegisterCsiHandler('t', func(params ansi.Params) bool {
		n, _, _ := params.Param(0, 0)
		cw, ch := cell()
		switch {
		case n == 16 && cw > 0 && ch > 0:
			reply(fmt.Sprintf("\x1b[6;%d;%dt", ch, cw))
		case n == 14 && cw > 0 && ch > 0:
			reply(fmt.Sprintf("\x1b[4;%d;%dt", ch*w.emu.Height(), cw*w.emu.Width()))
		case n == 18:
			reply(fmt.Sprintf("\x1b[8;%d;%dt", w.emu.Height(), w.emu.Width()))
		case n == 14 || n == 16:
			// The size in pixels isn't known: no reply, as a terminal
			// without it does.
		default:
			return false // other window operations: not supported
		}
		return true
	})
	w.emu.RegisterCsiHandler(ansi.Command('?', 0, 'n'), func(params ansi.Params) bool {
		if n, _, _ := params.Param(0, 0); n != 996 {
			return false // the emulator's own (cursor position, …)
		}
		w.reportScheme(true)
		return true
	})
}

// reportScheme tells the program whether the window is dark or light:
// always if asked, else only if it has mode 2031 set and the answer
// changed since it was last told.
func (w *Window) reportScheme(asked bool) {
	if w.emu == nil || w.schemeTone == nil || (!asked && !w.schemeReports) {
		return
	}
	t := w.schemeTone()
	if !asked && w.schemeSent == int(t)+1 {
		return
	}
	w.schemeSent = int(t) + 1
	n := 1 // dark
	if t == toneLight {
		n = 2
	}
	_, _ = fmt.Fprintf(w.emu.InputPipe(), "\x1b[?997;%dn", n)
}

// reportSchemes sends color scheme reports to the windows that asked for
// them and whose background changed (after a palette or the terminal's own
// colors changed).
func (m *WM) reportSchemes() {
	for _, w := range m.windows {
		w.reportScheme(false)
	}
}
