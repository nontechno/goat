package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/rivo/uniseg"
)

// The events window (Alt+-, keys.show_events) lists what happened that a
// terminal would otherwise show only fleetingly or not at all: bells,
// desktop notifications, clipboard writes, programs exiting, a window's
// user@host changing, config warnings and goat's own errors. Each entry has
// the time and the window it came from; repeats in a row are merged
// ("bell ×5").
//
// It is a regular floating window (move, resize, focus, pin, scroll back,
// select and copy, Alt+b colors), with no program behind it: only Alt+-
// hides it, and shows it again where it was. While it has the focus, keys
// other than shortcuts scroll it (arrows, PageUp/PageDown, Home/End) and
// are not sent anywhere. The status bar shows "!N" while it is hidden and
// N events came in.

type eventKind string

const (
	evBell      eventKind = "bell"
	evNotify    eventKind = "notify"
	evClipboard eventKind = "clipboard"
	evExit      eventKind = "exit"
	evIdentity  eventKind = "identity"
	evConfig    eventKind = "config"
	evError     eventKind = "error"
)

// event is one entry of the events window.
type event struct {
	at    time.Time
	win   string // "2:vim" at the time; "goat" for goat itself
	kind  eventKind
	text  string
	count int // merged repeats (1 = once)
}

const (
	maxEvents        = 1000 // oldest are dropped
	maxEventText     = 300  // characters kept of a notification
	eventMergeWindow = 10 * time.Second
)

// events is the events window's state. Owned by the event loop.
type events struct {
	list   []event
	win    *Window // the window (nil until first shown)
	unseen int     // added while it was hidden

	// What the window's emulator holds: synced entries, drawn with these
	// column widths, the last one with lastCount repeats. full forces a
	// redraw (entries dropped at the front).
	synced      int
	lastCount   int
	winW, kindW int
	full        bool
	changed     bool // the list changed since the window was updated
}

// logEvent records an event from w (nil = goat itself).
func (m *WM) logEvent(w *Window, kind eventKind, text string) {
	text = cleanEventText(text)
	who := "goat"
	if w != nil {
		who = fmt.Sprintf("%d:%s", m.number(w), w.displayTitle(m.cfg.TitleSource))
	}
	now := time.Now()
	ev := &m.ev
	if n := len(ev.list); n > 0 {
		last := &ev.list[n-1]
		if last.win == who && last.kind == kind && last.text == text && now.Sub(last.at) < eventMergeWindow {
			last.count++
			last.at = now
			m.eventAdded()
			return
		}
	}
	if len(ev.list) >= maxEvents {
		ev.list = append(ev.list[:0], ev.list[len(ev.list)-maxEvents+1:]...)
		ev.full = true
	}
	ev.list = append(ev.list, event{at: now, win: who, kind: kind, text: text, count: 1})
	m.eventAdded()
}

func (m *WM) eventAdded() {
	m.ev.changed = true
	if !m.eventsShown() {
		m.ev.unseen++
	}
	m.dirty = true
}

// cleanEventText drops control characters (a notification must not be able
// to send escape sequences to the real terminal) and shortens it.
func cleanEventText(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case !unicode.IsPrint(r):
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxEventText {
		s = string(r[:maxEventText-1]) + "…"
	}
	return s
}

// hookEvents routes w's bells and notifications to the events window.
func (m *WM) hookEvents(w *Window) {
	w.onEvent = func(kind eventKind, text string) {
		logger.Info("window event", "window", w.id, "kind", string(kind), "text", cleanEventText(text))
		m.logEvent(w, kind, text)
	}
}

// parseNotification turns a desktop-notification OSC into its text:
//
//	OSC 9 ; text                    (iTerm2, Windows Terminal, kitty, ...)
//	OSC 777 ; notify ; title ; body (urxvt, VTE)
//	OSC 99 ; metadata ; payload     (kitty; e=1 means base64)
//
// ok is false for other uses of the same numbers, such as ConEmu's OSC 9;4
// (progress) or OSC 9;9 (directory).
func parseNotification(osc []byte) (text string, ok bool) {
	cmd, rest, _ := strings.Cut(string(osc), ";")
	switch cmd {
	case "9":
		if first, _, _ := strings.Cut(rest, ";"); first != "" {
			if _, err := strconv.Atoi(first); err == nil {
				return "", false // ConEmu: 9;1 sleep, 9;4 progress, 9;9 cwd, ...
			}
		}
		return rest, strings.TrimSpace(rest) != ""
	case "777":
		parts := strings.SplitN(rest, ";", 3)
		if len(parts) < 2 || parts[0] != "notify" {
			return "", false
		}
		title, body := parts[1], ""
		if len(parts) == 3 {
			body = parts[2]
		}
		switch {
		case title == "":
			return body, body != ""
		case body == "":
			return title, true
		}
		return title + ": " + body, true
	case "99":
		meta, payload, found := strings.Cut(rest, ";")
		if !found || payload == "" {
			return "", false
		}
		part := "title"
		for _, kv := range strings.Split(meta, ":") {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "e":
				if v == "1" {
					b, err := base64.StdEncoding.DecodeString(payload)
					if err != nil {
						return "", false
					}
					payload = string(b)
				}
			case "p":
				part = v
			}
		}
		switch part {
		case "title":
			return payload, true
		case "body":
			return "(body) " + payload, true
		}
		return "", false // icons, buttons, queries
	}
	return "", false
}

// ---- the window ---------------------------------------------------------------

// isEvents reports whether w is the events window.
func (m *WM) isEvents(w *Window) bool { return w != nil && w == m.ev.win }

// eventsShown reports whether the events window is on screen.
func (m *WM) eventsShown() bool {
	return m.ev.win != nil && slices.Contains(m.windows, m.ev.win)
}

// toggleEvents shows the events window (on top, focused; where it was
// last time) or hides it.
func (m *WM) toggleEvents() {
	if m.eventsShown() {
		m.hideEvents()
		return
	}
	if m.ev.win == nil {
		m.ev.win = m.newEventsWindow()
	}
	w := m.ev.win
	m.place(w, w.x, w.y, w.w, w.h) // the screen may have changed size
	m.windows = append(m.windows, w)
	m.ev.unseen, m.ev.changed = 0, true
	m.focus(w)
}

// hideEvents takes the events window off screen; its place and contents
// are kept for the next Alt+-.
func (m *WM) hideEvents() {
	w := m.ev.win
	i := slices.Index(m.windows, w)
	if i < 0 {
		return
	}
	m.clearSelectionIn(w)
	m.windows = slices.Delete(m.windows, i, i+1)
	if m.drag.win == w {
		m.drag = drag{}
	}
	if m.focused == w {
		m.focused = nil
		if n := len(m.windows); n > 0 {
			m.focus(m.windows[n-1])
		}
	}
	m.dirty = true
}

// newEventsWindow makes the window: a frame and an emulator holding the
// list, no program. Centered, most of the width, up to 30 rows.
func (m *WM) newEventsWindow() *Window {
	area := m.areaH()
	width := min(m.cols, max(min(m.cols-4, 100), m.cols*4/5))
	height := min(area, max(min(area-2, 30), area*3/5))
	w := &Window{
		id: -1, x: (m.cols - width) / 2, y: (area - height) / 2, w: width, h: height,
		decorated: !m.cfg.DisableMouse, frame: m.cfg.frame,
		shellName: "events", nowrapW: m.cfg.NowrapWidth, nowrap: !m.cfg.WrapLines,
		mouseModes: map[ansi.DECMode]bool{}, colorIndex: -1,
		// no cursor, no input queue, no PTY
	}
	w.emu = vt.NewEmulator(w.emuWidth(w.contentW()), w.contentH())
	w.emu.SetScrollbackSize(maxEvents + 100)
	go func() { _, _ = io.Copy(io.Discard, w.emu) }() // replies go nowhere
	m.initBackground(w)
	m.applyHostColors(w)
	m.ev.full, m.ev.changed = true, true
	logger.Info("events window opened")
	return w
}

// syncEvents brings the window's contents up to date with the list:
// usually by appending lines (or rewriting the last one, when a repeat was
// merged into it), else by redrawing it all. Called before drawing.
func (m *WM) syncEvents() {
	ev := &m.ev
	w := ev.win
	if w == nil || !ev.changed || !m.eventsShown() {
		return
	}
	ev.changed = false
	winW, kindW := 0, 0
	for _, e := range ev.list {
		winW = max(winW, min(uniseg.StringWidth(e.win), 20))
		kindW = max(kindW, kindWidth(e))
	}
	redraw := ev.full || ev.synced == 0 || ev.synced > len(ev.list) || winW != ev.winW || kindW != ev.kindW
	var b strings.Builder
	if !redraw && ev.list[ev.synced-1].count != ev.lastCount {
		// A repeat was merged into the last line: rewrite it, if it is one row.
		e := ev.list[ev.synced-1]
		line := eventLine(e, winW, kindW)
		if !w.nowrap && uniseg.StringWidth(line) > w.contentW() {
			redraw = true
		} else {
			b.WriteString("\r\x1b[2K" + eventSGR(e, line))
		}
	}
	if redraw {
		_ = w.feed([]byte("\x1b[0m\x1b[H\x1b[2J"))
		w.emu.ClearScrollback()
		w.scroll = 0
		b.Reset()
		if len(ev.list) == 0 {
			b.WriteString("Nothing yet. Bells, desktop notifications, clipboard writes,\r\n" +
				"programs exiting and user@host changes are listed here.")
		}
		for i, e := range ev.list {
			if i > 0 {
				b.WriteString("\r\n")
			}
			b.WriteString(eventSGR(e, eventLine(e, winW, kindW)))
		}
	} else {
		for _, e := range ev.list[ev.synced:] {
			b.WriteString("\r\n" + eventSGR(e, eventLine(e, winW, kindW)))
		}
	}
	_ = w.feed([]byte(b.String())) // (a scrolled-back view stays in place)
	ev.synced, ev.full, ev.winW, ev.kindW = len(ev.list), false, winW, kindW
	if n := len(ev.list); n > 0 {
		ev.lastCount = ev.list[n-1].count
	}
}

// eventSGR wraps an entry's line in its style: bold for what asks for
// attention (bells, notifications, errors).
func eventSGR(e event, line string) string {
	switch e.kind {
	case evBell, evNotify, evError:
		return "\x1b[1m" + line + "\x1b[0m"
	}
	return line
}

// eventsKey handles a key for the focused events window: scrolling keys
// scroll it; nothing is sent anywhere.
func (m *WM) eventsKey(w *Window, k uv.Key) {
	page := max(w.contentH()-1, 1)
	switch k.Code {
	case uv.KeyUp:
		m.scrollWindow(w, 1)
	case uv.KeyDown:
		m.scrollWindow(w, -1)
	case uv.KeyPgUp:
		m.scrollWindow(w, page)
	case uv.KeyPgDown:
		m.scrollWindow(w, -page)
	case uv.KeyHome:
		m.scrollWindow(w, w.emu.ScrollbackLen())
	case uv.KeyEnd:
		m.scrollWindow(w, -w.scroll)
	}
}

func kindWidth(e event) int {
	n := len(e.kind)
	if e.count > 1 {
		n += uniseg.StringWidth(fmt.Sprintf(" ×%d", e.count))
	}
	return n
}

// eventLine formats one entry: "18:26:03  2:vim  bell ×3  text", with the
// window and kind columns padded to winW and kindW.
func eventLine(e event, winW, kindW int) string {
	kind := string(e.kind)
	if e.count > 1 {
		kind += fmt.Sprintf(" ×%d", e.count)
	}
	s := e.at.Format("15:04:05") + "  " + padRight(e.win, winW) + "  " + padRight(kind, kindW)
	if e.text != "" {
		s += "  " + e.text
	}
	return strings.TrimRight(s, " ")
}

func padRight(s string, w int) string {
	if n := uniseg.StringWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// eventsIndicator is the status bar's "!N": events that came in while the
// events window was hidden ("" if none).
func (m *WM) eventsIndicator() string {
	if m.ev.unseen == 0 || m.eventsShown() {
		return ""
	}
	return fmt.Sprintf(" !%d ", m.ev.unseen)
}
