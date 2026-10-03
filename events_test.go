package main

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

func TestParseNotification(t *testing.T) {
	for _, tt := range []struct {
		osc  string
		text string
		ok   bool
	}{
		{"9;build done", "build done", true},
		{"9;4;1;50", "", false}, // ConEmu progress
		{"9;9;/home/x", "", false},
		{"9;", "", false},
		{"777;notify;Title;Body text", "Title: Body text", true},
		{"777;notify;Only title", "Only title", true},
		{"777;notify;;Body", "Body", true},
		{"777;preexec", "", false},
		{"99;;Hello", "Hello", true},
		{"99;i=1:d=0;Hi there", "Hi there", true},
		{"99;i=1:p=body;the body", "(body) the body", true},
		{"99;e=1;SGVsbG8=", "Hello", true},
		{"99;p=icon;xxx", "", false},
		{"99;e=1;!!!", "", false},
	} {
		text, ok := parseNotification([]byte(tt.osc))
		if text != tt.text || ok != tt.ok {
			t.Errorf("%q: got %q %v, want %q %v", tt.osc, text, ok, tt.text, tt.ok)
		}
	}
}

func TestEventLogMergesCapsAndCleans(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 80, 24)
	w := fakeFramed(1, 0, 0, 20, 6, true, frameCompact)
	m.windows = []*Window{w}
	for range 3 {
		m.logEvent(w, evBell, "")
	}
	m.logEvent(w, evNotify, "a\x1b]0;evil\x07b\nc")
	if n := len(m.ev.list); n != 2 {
		t.Fatalf("%d entries, want 2 (bells merged)", n)
	}
	if e := m.ev.list[0]; e.count != 3 || e.win != "1:bash" || e.kind != evBell {
		t.Errorf("merged bell %+v", e)
	}
	if got := m.ev.list[1].text; got != "a]0;evilb c" {
		t.Errorf("control characters kept: %q", got)
	}
	if m.ev.unseen != 4 || m.eventsIndicator() != " !4 " {
		t.Errorf("unseen %d, indicator %q", m.ev.unseen, m.eventsIndicator())
	}
	for i := range maxEvents + 50 {
		m.logEvent(nil, evError, strings.Repeat("x", i%7+1)+string(rune('a'+i%26)))
	}
	if n := len(m.ev.list); n != maxEvents {
		t.Errorf("%d entries kept, want %d", n, maxEvents)
	}
	if long := cleanEventText(strings.Repeat("é", 1000)); len([]rune(long)) != maxEventText {
		t.Errorf("long text kept %d characters", len([]rune(long)))
	}
}

// Alt+- shows it as a regular window and only Alt+- hides it (not Esc,
// q, Alt+x or a click elsewhere); it comes back where it was. Keys typed
// into it go nowhere; other windows keep their numbers.
func TestEventsWindow(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 100, 30)
	a := fakeFramed(1, 0, 0, 40, 10, true, frameCompact)
	b := fakeFramed(2, 50, 0, 40, 10, true, frameCompact)
	m.windows = []*Window{a, b}
	m.focus(a)
	m.logEvent(a, evNotify, "build done")
	m.logEvent(a, evBell, "")
	if m.eventsIndicator() != " !2 " {
		t.Errorf("indicator %q", m.eventsIndicator())
	}

	alt := func(r rune) { _ = m.handleKey(uv.Key{Code: r, Mod: uv.ModAlt}) }
	alt('-')
	ew := m.ev.win
	if !m.eventsShown() || m.focused != ew {
		t.Fatal("Alt+- did not show and focus the events window")
	}
	if m.eventsIndicator() != "" || m.number(b) != 2 || m.titleLabel(ew) != "events" {
		t.Errorf("indicator %q, number of b %d, title %q", m.eventsIndicator(), m.number(b), m.titleLabel(ew))
	}
	screen := strings.Join(render(m), "\n")
	for _, want := range []string{"events", "1:bash  notify  build done", "1:bash  bell"} {
		if !strings.Contains(screen, want) {
			t.Errorf("%q not on screen:\n%s", want, screen)
		}
	}

	// Not closed by Esc, q, Alt+x or clicks; typing and pasting go nowhere.
	before := pendingInput(a) + pendingInput(b)
	_ = m.handleKey(uv.Key{Code: uv.KeyEscape})
	m.escExpired()
	_ = m.handleKey(uv.Key{Code: 'q', Text: "q"})
	alt('x')
	m.paste("rm -rf /\n")
	if !m.eventsShown() {
		t.Fatal("events window closed by Esc, q or Alt+x")
	}
	if pendingInput(a)+pendingInput(b) != before {
		t.Error("input typed into the events window reached a program")
	}
	m.handleMouse(uv.MouseClickEvent{X: 1, Y: 1, Button: uv.MouseLeft}) // focus another window
	if !m.eventsShown() || m.focused != a {
		t.Fatal("clicking another window hid the events window, or didn't focus it")
	}
	m.logEvent(b, evBell, "")
	if m.eventsIndicator() != "" {
		t.Error("events counted as unseen while the window is shown")
	}
	m.focus(ew) // (window 1, raised by the click, covers part of it)
	if !strings.Contains(strings.Join(render(m), "\n"), "2:bash  bell") {
		t.Error("new event not shown in the open window")
	}

	// Move it, hide it (from another window), show it again: same place.
	m.place(ew, 5, 4, 60, 12)
	alt('-')
	if m.eventsShown() || slices.Contains(m.windows, ew) || m.focused == ew {
		t.Fatal("Alt+- did not hide the events window")
	}
	m.logEvent(a, evExit, "exit status 0")
	if m.eventsIndicator() != " !1 " {
		t.Errorf("indicator %q", m.eventsIndicator())
	}
	alt('-')
	if ew != m.ev.win || ew.x != 5 || ew.y != 4 || ew.w != 60 || ew.h != 12 {
		t.Errorf("shown again at %d,%d %dx%d", ew.x, ew.y, ew.w, ew.h)
	}
	if !strings.Contains(strings.Join(render(m), "\n"), "exit status 0") {
		t.Error("event that came in while hidden is missing")
	}
	m.closeAll() // quitting with it open is fine
}

// Many events: the newest is in view, older ones are in its history, and
// scrolling keys scroll it while it has the focus.
func TestEventsWindowScroll(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 60, 14)
	for i := range 40 {
		m.logEvent(nil, evError, fmt.Sprintf("entry %02d", i))
	}
	m.toggleEvents()
	w := m.ev.win
	screen := strings.Join(render(m), "\n")
	if !strings.Contains(screen, "entry 39") || strings.Contains(screen, "entry 00") {
		t.Fatalf("newest entry not in view:\n%s", screen)
	}
	_ = m.handleKey(uv.Key{Code: uv.KeyHome})
	if !strings.Contains(strings.Join(render(m), "\n"), "entry 00") || w.scroll == 0 {
		t.Error("Home: oldest entry not shown")
	}
	m.logEvent(nil, evError, "entry 40")
	if s := strings.Join(render(m), "\n"); !strings.Contains(s, "entry 00") {
		t.Errorf("a new event moved the scrolled-back view:\n%s", s)
	}
	_ = m.handleKey(uv.Key{Code: uv.KeyEnd})
	if !strings.Contains(strings.Join(render(m), "\n"), "entry 40") || w.scroll != 0 {
		t.Error("End: newest entry not shown")
	}
	for range 3 {
		m.logEvent(nil, evBell, "")
	}
	if !strings.Contains(strings.Join(render(m), "\n"), "bell ×3") {
		t.Error("merged repeat not updated in place")
	}
	if n := strings.Count(strings.Join(render(m), "\n"), "bell"); n != 1 {
		t.Errorf("bell shown %d times", n)
	}
}

// End to end: a program's bells and notifications are recorded against its
// window; ConEmu's progress OSC 9;4 is not a notification.
func TestEventsFromProgram(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	m := newWM(DefaultConfig(), nil, 80, 24)
	script := `printf '\a\a\a\033]9;build done\a\033]9;4;1;50\a\033]777;notify;Make;finished\a'; sleep 5`
	path := t.TempDir() + "/run.sh" // (the shell command is split on spaces)
	if err := os.WriteFile(path, []byte(script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := newWindow(1, "/bin/sh "+path, 0, 0, 40, 10, winOpts{}, m.out)
	if err != nil {
		t.Fatal(err)
	}
	m.hookEvents(w)
	m.windows = []*Window{w}
	defer m.closeAll()

	deadline := time.Now().Add(5 * time.Second)
	for len(m.ev.list) < 3 && time.Now().Before(deadline) {
		select {
		case msg := <-m.out:
			m.handlePty(msg)
		case <-time.After(50 * time.Millisecond):
		}
	}
	var got []string
	for _, e := range m.ev.list {
		got = append(got, eventLine(e, 0, 0)[10:]) // without the time
	}
	want := []string{"1:sh  bell ×3", "1:sh  notify  build done", "1:sh  notify  Make: finished"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("events:\n got %q\nwant %q", got, want)
	}
}

func TestShowEventsKeyConfig(t *testing.T) {
	c, warns := parseConfig("[keys]\nshow_events = \"e\"\n")
	if len(warns) != 0 || c.bindings['e'] != actShowEvents {
		t.Errorf("show_events = e: %v %v", c.bindings['e'], warns)
	}
	if _, bound := c.bindings['-']; bound {
		t.Error("old key still bound")
	}
	if d := DefaultConfig(); d.bindings['-'] != actShowEvents {
		t.Error("Alt+- not bound by default")
	}
}

func pendingInput(w *Window) int {
	w.in.mu.Lock()
	defer w.in.mu.Unlock()
	return len(w.in.buf)
}
