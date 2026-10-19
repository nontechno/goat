package main

import (
	"fmt"
	"time"
)

// Hiding the floating windows: Alt+0 focuses the background shell; pressed
// again while the background has the focus, it hides every floating window
// (minimizes them), and pressed once more it brings back the ones still
// hidden. A hidden window keeps running, its place, size and number; it
// isn't drawn and can't be clicked, and its status bar tab shows 👻 before
// its name ("👻2:vim"). Focusing it in any way brings it back alone: Alt+N
// (its number), a click on its tab. Alt+n / Alt+p skip hidden windows.
//
// The events window isn't affected: only Alt+- shows and hides it.

// (ghost, the hidden window's tab mark: iconography.go)

// statusShort is how long Alt+0's message stays in the status bar.
const statusShort = 3 * time.Second

// hideable are the windows Alt+0 hides: the floating ones, not the events
// window, in number order.
func (m *WM) hideable() []*Window {
	return m.byID() // byID leaves out the background and the events window
}

// toggleHidden is Alt+0 with the background focused: bring back the hidden
// windows if there are any, else hide all the floating ones.
func (m *WM) toggleHidden() {
	var hidden, shown []*Window
	for _, w := range m.hideable() {
		if w.minimized {
			hidden = append(hidden, w)
		} else {
			shown = append(shown, w)
		}
	}
	switch {
	case len(hidden) > 0:
		for _, w := range hidden {
			w.minimized = false
		}
		m.setBarMessage(fmt.Sprintf("%s %s back", plural(len(hidden), "window"), pick(len(hidden) == 1, "is", "are")), statusShort)
	case len(shown) > 0:
		for _, w := range shown {
			m.hide(w)
		}
		m.setBarMessage(fmt.Sprintf("%s hidden: Alt+0 brings them back, Alt+N or a click on its tab one", plural(len(shown), "window")), statusShort)
	default:
		m.setBarMessage("no windows to hide", statusShort)
	}
	m.dirty = true
}

// hide minimizes w. The focus, if w had it, goes to the background.
func (m *WM) hide(w *Window) {
	w.minimized = true
	m.clearSelectionIn(w)
	if m.drag.win == w {
		m.drag = drag{}
	}
	if m.focused == w {
		m.focus(m.topShown())
	}
	m.dirty = true
}

// topShown is the topmost window on screen (nil if none).
func (m *WM) topShown() *Window {
	for i := len(m.windows) - 1; i >= 0; i-- {
		if w := m.windows[i]; !w.minimized {
			return w
		}
	}
	return nil
}

// shownCycle is the Alt+n / Alt+p order without the hidden windows.
func (m *WM) shownCycle() []*Window {
	var ws []*Window
	for _, w := range m.cycle() {
		if !w.minimized {
			ws = append(ws, w)
		}
	}
	return ws
}

// plural is "1 window", "3 windows".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
