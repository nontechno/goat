package main

import (
	"slices"

	uv "github.com/charmbracelet/ultraviolet"
)

// Scrolling back and the mouse: clicks, drags, wheel, selection.

// ---- scrollback -------------------------------------------------------------

func (m *WM) focusedPage() int {
	if m.focused == nil {
		return 0
	}
	return m.focused.contentH() - 1
}

func (m *WM) scrollFocused(n int) {
	if m.focused != nil {
		m.scrollWindow(m.focused, n)
	}
}

// scrollWindow moves the history view by n lines (positive = older).
func (m *WM) scrollWindow(w *Window, n int) {
	if w.emu.IsAltScreen() {
		return
	}
	s := clamp(w.scroll+n, 0, w.emu.ScrollbackLen())
	if s != w.scroll {
		w.scroll = s
		m.dirty = true
	}
}

// ---- mouse ------------------------------------------------------------------

// forwardMouse passes a mouse event to w's program in content coordinates.
func (m *WM) forwardMouse(w *Window, ev uv.MouseEvent) {
	if w.closed || !w.wantsMouse() {
		return
	}
	mo := ev.Mouse()
	mo.X = clamp(mo.X-w.contentX(), 0, w.contentW()-1)
	mo.Y = clamp(mo.Y-w.contentY(), 0, w.contentH()-1)
	switch ev.(type) {
	case uv.MouseClickEvent:
		w.emu.SendMouse(uv.MouseClickEvent(mo))
	case uv.MouseReleaseEvent:
		w.emu.SendMouse(uv.MouseReleaseEvent(mo))
	case uv.MouseMotionEvent:
		w.emu.SendMouse(uv.MouseMotionEvent(mo))
	case uv.MouseWheelEvent:
		w.emu.SendMouse(uv.MouseWheelEvent(mo))
	}
}

func (m *WM) handleMouse(ev uv.MouseEvent) {
	if m.cfg.DisableMouse {
		return
	}
	if m.settings != nil {
		m.settingsMouse(ev)
		return
	}
	if m.quitAsk != nil {
		m.quitDialogMouse(ev)
		return
	}
	if m.palDlg != nil {
		m.paletteMouse(ev)
		return
	}
	mo := ev.Mouse()
	x, y := mo.X, mo.Y

	switch ev.(type) {
	case uv.MouseClickEvent:
		if y == m.rows-1 { // status bar: click a tab to focus that window
			for _, t := range m.tabs {
				if x >= t.x0 && x < t.x1 && slices.Contains(m.windows, t.win) {
					m.focus(t.win)
				}
			}
			return
		}
		w := m.topAt(x, y)
		if w == nil {
			return
		}
		m.focus(w)
		d := drag{win: w}
		shift := mo.Mod.Contains(uv.ModShift)
		switch {
		case mo.Button == uv.MouseLeft && w.onGrip(x, y):
			// Frame "none": the corner resizes, even over a program that
			// uses the mouse.
			d.kind = dragBottomR
		case w.inContent(x, y) && w.wantsMouse() && !shift:
			d.kind = dragContent
			m.forwardMouse(w, ev)
		case w.inContent(x, y):
			// Select text; Shift overrides programs that use the mouse.
			if mo.Button == uv.MouseLeft {
				m.startSelection(w, x, y)
				if m.isInventory(w) { // choose the window clicked (inventory.go)
					m.inventoryClick(m.sel.anchor.line)
				}
				if m.isHelp(w) { // choose the shortcut clicked (keymap.go)
					m.helpClick(m.sel.anchor.line)
				}
				return
			}
		case mo.Button != uv.MouseLeft:
		case w.onHandle(x, y) < 0: // frame "none": left end of the top line
			d.kind, d.anchorR = dragBottomL, w.x+w.w
		case w.onHandle(x, y) > 0: // ... right end
			d.kind = dragBottomR
		case w.inTitleBar(x, y):
			d.kind, d.offX, d.offY = dragMove, x-w.x, y-w.y
		case w.onBottom(x, y) && w.onLeft(x, y):
			d.kind, d.anchorR = dragBottomL, w.x+w.w
		case w.onBottom(x, y) && w.onRight(x, y):
			d.kind = dragBottomR
		case w.onLeft(x, y):
			d.kind, d.anchorR = dragLeft, w.x+w.w
		case w.onRight(x, y):
			d.kind = dragRight
		case w.onBottom(x, y):
			d.kind = dragBottom
		}
		d.startX, d.startY, d.startW, d.startH = x, y, w.w, w.h
		if d.kind != dragNone {
			m.drag = d
		}

	case uv.MouseMotionEvent:
		d := m.drag
		w := d.win
		if w == nil {
			return
		}
		switch d.kind {
		case dragContent:
			m.forwardMouse(w, ev)
		case dragSelect:
			m.extendSelection(x, y)
		case dragMove:
			m.place(w, x-d.offX, y-d.offY, w.w, w.h)
		case dragRight:
			m.place(w, w.x, w.y, d.startW+x-d.startX, w.h)
		case dragBottom:
			m.place(w, w.x, w.y, w.w, d.startH+y-d.startY)
		case dragBottomR:
			m.place(w, w.x, w.y, d.startW+x-d.startX, d.startH+y-d.startY)
		case dragLeft, dragBottomL:
			nx := min(d.anchorR-d.startW+x-d.startX, d.anchorR-m.minW())
			h := w.h
			if d.kind == dragBottomL {
				h = d.startH + y - d.startY
			}
			m.place(w, nx, w.y, d.anchorR-nx, h)
		}

	case uv.MouseReleaseEvent:
		if m.drag.kind == dragContent && m.drag.win != nil {
			m.forwardMouse(m.drag.win, ev)
		}
		if m.drag.kind == dragSelect {
			m.finishSelection()
		}
		m.drag = drag{}

	case uv.MouseWheelEvent:
		w := m.topAt(x, y)
		if w == nil || y == m.rows-1 {
			return
		}
		sideways := mo.Button == uv.MouseWheelLeft || mo.Button == uv.MouseWheelRight ||
			mo.Mod.Contains(uv.ModShift)
		switch {
		case w.inContent(x, y) && w.wantsMouse() && !mo.Mod.Contains(uv.ModShift):
			m.forwardMouse(w, ev)
		case sideways:
			// Shift+wheel or a sideways wheel: scroll a no-wrap view.
			n := 8
			if mo.Button == uv.MouseWheelUp || mo.Button == uv.MouseWheelLeft {
				n = -n
			}
			if w.scrollSideways(n) {
				m.dirty = true
			}
		case w.emu.IsAltScreen():
			// Like xterm's alternate-scroll mode: wheel = arrow keys, so
			// less, man and friends scroll.
			if k := wheelKey(mo.Button); k != 0 {
				for range 3 {
					w.send(w.encodeKey(uv.Key{Code: k}))
				}
			}
		default:
			switch mo.Button {
			case uv.MouseWheelUp:
				m.scrollWindow(w, 3)
			case uv.MouseWheelDown:
				m.scrollWindow(w, -3)
			}
		}
	}
}

func wheelKey(b uv.MouseButton) rune {
	switch b {
	case uv.MouseWheelUp:
		return uv.KeyUp
	case uv.MouseWheelDown:
		return uv.KeyDown
	}
	return 0
}
