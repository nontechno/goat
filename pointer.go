package main

import "strings"

// Mouse pointer shapes (OSC 22): goat sets the pointer for what is under
// it: an arrow over frames, title bars, the status bar and programs that
// use the mouse; a text beam over text that can be selected; resize arrows
// over the edges and corners that resize a window (and while dragging
// them), a move cross while moving one.
//
// Only some terminals take OSC 22 (kitty, ghostty and foot with CSS
// cursor names; iTerm2 3.5+ and xterm with X11 ones); others (macOS
// Terminal, tmux) show their own pointer whatever goat asks: there, over
// a program using the mouse, typically the text beam everywhere. To know
// what is under the pointer without a button pressed, goat then asks the
// terminal for every mouse movement (mode 1003), not only drags.
//
// config.toml's pointer_shapes: "auto" (the terminals above, as their
// environment shows them), "on" or "off". Unless it is "off", goat sets
// the arrow when it starts (to a terminal not known for OSC 22 too, in
// both namings, in case it takes one) and the text beam when it ends.

type pointerShape int

const (
	ptrUnknown pointerShape = iota // not set yet (or not known after a repaint)
	ptrDefault                     // the arrow
	ptrText                        // the text beam
	ptrEW                          // ↔ left / right edge
	ptrNS                          // ↕ bottom edge
	ptrNWSE                        // ⤡ bottom-right corner
	ptrNESW                        // ⤢ bottom-left corner
	ptrMove                        // moving a window
)

// pointerNames are a shape's names in one naming scheme.
type pointerNames [ptrMove + 1]string

var (
	cssPointers = pointerNames{"", "default", "text", "ew-resize", "ns-resize", "nwse-resize", "nesw-resize", "move"}
	x11Pointers = pointerNames{"", "left_ptr", "xterm", "sb_h_double_arrow", "sb_v_double_arrow",
		"bottom_right_corner", "bottom_left_corner", "fleur"}
)

// hostPointers decides whether goat sets pointer shapes in the terminal
// it runs in, and with which names (nil: it doesn't), and why.
func hostPointers(setting string, env []string) (*pointerNames, string) {
	get := func(k string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, k+"="); ok {
				return v
			}
		}
		return ""
	}
	term, prog := get("TERM"), get("TERM_PROGRAM")
	x11 := prog == "iTerm.app" || get("LC_TERMINAL") == "iTerm2" || get("XTERM_VERSION") != ""
	switch setting {
	case "off":
		return nil, "pointer_shapes = \"off\""
	case "on":
		if x11 {
			return &x11Pointers, "pointer_shapes = \"on\" (X11 names: iTerm2 / xterm)"
		}
		return &cssPointers, "pointer_shapes = \"on\""
	}
	switch {
	case strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux"):
		return nil, "in tmux / screen (they don't pass OSC 22 on)"
	case x11 && (prog == "iTerm.app" || get("LC_TERMINAL") == "iTerm2"):
		return &x11Pointers, "iTerm2"
	case x11:
		return &x11Pointers, "xterm"
	case term == "xterm-kitty" || get("KITTY_WINDOW_ID") != "":
		return &cssPointers, "kitty"
	case term == "xterm-ghostty" || prog == "ghostty":
		return &cssPointers, "ghostty"
	case strings.HasPrefix(term, "foot"):
		return &cssPointers, "foot"
	}
	return nil, "the terminal isn't known to take OSC 22"
}

// pointerAt is the shape for the screen point (x, y), or for the drag
// going on.
func (m *WM) pointerAt(x, y int) pointerShape {
	if m.settings != nil || m.quitAsk != nil || m.palDlg != nil {
		return ptrDefault
	}
	switch m.drag.kind {
	case dragMove:
		return ptrMove
	case dragLeft, dragRight:
		return ptrEW
	case dragBottom:
		return ptrNS
	case dragBottomR:
		return ptrNWSE
	case dragBottomL:
		return ptrNESW
	case dragSelect:
		return ptrText
	case dragContent:
		return ptrDefault
	}
	w := m.topAt(x, y)
	switch {
	case w == nil || y == m.rows-1:
		return ptrDefault
	case w.onGrip(x, y) || w.onHandle(x, y) > 0:
		return ptrNWSE
	case w.onHandle(x, y) < 0:
		return ptrNESW
	case w.inContent(x, y) && w.wantsMouse():
		return ptrDefault // the program's: mc, vim with the mouse on
	case w.inContent(x, y):
		return ptrText
	case w.inTitleBar(x, y):
		return ptrDefault
	case w.onBottom(x, y) && w.onLeft(x, y):
		return ptrNESW
	case w.onBottom(x, y) && w.onRight(x, y):
		return ptrNWSE
	case w.onLeft(x, y) || w.onRight(x, y):
		return ptrEW
	case w.onBottom(x, y):
		return ptrNS
	}
	return ptrDefault
}

// updatePointer sets the pointer for (x, y), drawn with the next frame.
func (m *WM) updatePointer(x, y int) {
	if !m.pointerOn {
		return
	}
	if p := m.pointerAt(x, y); p != m.pointer {
		m.pointer = p
		m.dirty = true
	}
}

// pointerInfo says whether goat sets pointer shapes, and why (Alt+F2).
func (m *WM) pointerInfo() string {
	if m.pointerOn {
		return "shapes set (OSC 22): " + m.pointerWhy
	}
	return "the terminal's own: " + m.pointerWhy
}
