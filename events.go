package main

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/rivo/uniseg"
)

// The events window (Alt+-, keys.show_events) lists what happened that a
// terminal would otherwise show only fleetingly or not at all: bells,
// desktop notifications, clipboard writes, programs exiting, a window's
// user@host changing, config warnings and goat's own errors. Each entry has
// the time and the window it came from; repeats in a row are merged
// ("bell ×5").
//
// While it is shown it covers the middle of the screen: Esc, q or Alt+-
// close it; arrows, PageUp/PageDown, Home/End scroll it. Other Alt
// shortcuts keep working; plain keys and pastes don't reach programs. The
// status bar shows "!N" while N events are unseen.

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
	shown  bool
	scroll int // lines scrolled up from the newest
	unseen int // added while not shown
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
	}
	ev.list = append(ev.list, event{at: now, win: who, kind: kind, text: text, count: 1})
	if ev.shown && ev.scroll > 0 {
		ev.scroll++ // keep the lines being read in place
	}
	m.eventAdded()
}

func (m *WM) eventAdded() {
	if !m.ev.shown {
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

// ---- showing ----------------------------------------------------------------

func (m *WM) toggleEvents() {
	m.ev.shown = !m.ev.shown
	m.ev.scroll, m.ev.unseen = 0, 0
	m.drag = drag{}
	m.dirty = true
}

// eventsKey handles a key while the events window is shown.
func (m *WM) eventsKey(k uv.Key) error {
	if k.Mod.Contains(uv.ModAlt) {
		_, err := m.shortcut(k) // the window manager still works (Alt+- closes)
		return err
	}
	page := max(m.eventsBox().h-3, 1)
	switch {
	case k.Code == uv.KeyEscape, k.Code == 'q' && k.Mod == 0:
		m.toggleEvents()
	case k.Code == uv.KeyUp, k.Code == 'k' && k.Mod == 0:
		m.scrollEvents(1)
	case k.Code == uv.KeyDown, k.Code == 'j' && k.Mod == 0:
		m.scrollEvents(-1)
	case k.Code == uv.KeyPgUp:
		m.scrollEvents(page)
	case k.Code == uv.KeyPgDown:
		m.scrollEvents(-page)
	case k.Code == uv.KeyHome, k.Code == 'g' && k.Mod == 0:
		m.scrollEvents(len(m.ev.list))
	case k.Code == uv.KeyEnd, k.Code == 'G' || (k.Code == 'g' && k.Mod == uv.ModShift):
		m.scrollEvents(-len(m.ev.list))
	}
	return nil // anything else is not for the programs underneath
}

func (m *WM) scrollEvents(n int) {
	s := clamp(m.ev.scroll+n, 0, max(len(m.ev.list)-max(m.eventsBox().h-2, 1), 0))
	if s != m.ev.scroll {
		m.ev.scroll = s
		m.dirty = true
	}
}

// eventsMouse handles the mouse while the events window is shown: the
// wheel scrolls it, a click outside it closes it.
func (m *WM) eventsMouse(ev uv.MouseEvent) {
	mo := ev.Mouse()
	b := m.eventsBox()
	inside := mo.X >= b.x && mo.X < b.x+b.w && mo.Y >= b.y && mo.Y < b.y+b.h
	switch ev.(type) {
	case uv.MouseWheelEvent:
		switch mo.Button {
		case uv.MouseWheelUp:
			m.scrollEvents(3)
		case uv.MouseWheelDown:
			m.scrollEvents(-3)
		}
	case uv.MouseClickEvent:
		if !inside {
			m.toggleEvents()
		}
	}
}

type box struct{ x, y, w, h int }

// eventsBox is where the events window goes: centered above the status
// bar, most of its width, and as tall as the list (up to most of its
// height).
func (m *WM) eventsBox() box {
	area := m.areaH()
	w := min(m.cols, max(min(m.cols-4, 100), m.cols*4/5))
	maxH := min(area, max(min(area-2, 30), area*3/5))
	h := clamp(len(m.ev.list)+2, min(6, maxH), maxH)
	return box{x: (m.cols - w) / 2, y: (area - h) / 2, w: w, h: h}
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

// drawEvents draws the events window, newest entry at the bottom.
func (m *WM) drawEvents(scr uv.Screen) {
	if !m.ev.shown {
		return
	}
	b := m.eventsBox()
	if b.w < 4 || b.h < 3 {
		return
	}
	th := m.cfg.Theme
	base := uv.Style{Fg: th.StatusFg.C, Bg: th.StatusBg.C}
	border := uv.Style{Fg: th.FocusedBorder.C, Bg: th.StatusBg.C, Attrs: uv.AttrBold}
	x1, y1 := b.x+b.w-1, b.y+b.h-1
	fill := &uv.Cell{Content: " ", Width: 1, Style: base}
	for y := b.y; y <= y1; y++ {
		for x := b.x; x <= x1; x++ {
			put(scr, x, y, fill)
		}
		put(scr, b.x, y, runeCell('│', border))
		put(scr, x1, y, runeCell('│', border))
	}
	for x := b.x + 1; x < x1; x++ {
		put(scr, x, b.y, runeCell('─', border))
		put(scr, x, y1, runeCell('─', border))
	}
	put(scr, b.x, b.y, runeCell('╭', border))
	put(scr, x1, b.y, runeCell('╮', border))
	put(scr, b.x, y1, runeCell('╰', border))
	put(scr, x1, y1, runeCell('╯', border))

	title := fmt.Sprintf(" events (%d) ", len(m.ev.list))
	end := putCells(scr, b.x+2, b.y, textCells(title, border, b.w-4))
	hint := " Esc: close  ↑↓ PgUp PgDn: scroll "
	if hw := len([]rune(hint)); end+1+hw <= x1-1 {
		hst := base
		hst.Fg = th.HintText.C
		putCells(scr, x1-1-hw, y1, textCells(hint, hst, hw))
	}

	rows, textW := b.h-2, b.w-4
	list := m.ev.list
	if len(list) == 0 {
		hst := base
		hst.Fg = th.HintText.C
		putCells(scr, b.x+2, b.y+1, textCells("Nothing yet. Bells, desktop notifications, clipboard writes,", hst, textW))
		putCells(scr, b.x+2, b.y+2, textCells("programs exiting and user@host changes are listed here.", hst, textW))
		return
	}
	last := len(list) - m.ev.scroll // exclusive
	first := max(last-rows, 0)
	y := b.y + 1 + rows - (last - first) // newest at the bottom
	winW, kindW := 0, 0                  // align the columns of the lines shown
	for _, e := range list[first:last] {
		winW = max(winW, min(uniseg.StringWidth(e.win), 20))
		kindW = max(kindW, len(e.kind)+pick(e.count > 1, len(fmt.Sprintf(" ×%d", e.count))-1, 0))
	}
	for _, e := range list[first:last] {
		st := base
		switch e.kind {
		case evBell, evNotify, evError:
			st.Fg = th.HintText.C
		}
		putCells(scr, b.x+2, y, textCells(eventLine(e, winW, kindW), st, textW))
		y++
	}
	if m.ev.scroll > 0 {
		more := fmt.Sprintf(" ↓ %d newer ", m.ev.scroll)
		putCells(scr, b.x+2, y1, textCells(more, border, textW))
	}
}

// eventsIndicator is the status bar's "!N" for unseen events ("" if none).
func (m *WM) eventsIndicator() string {
	if m.ev.unseen == 0 || m.ev.shown {
		return ""
	}
	return fmt.Sprintf(" !%d ", m.ev.unseen)
}
