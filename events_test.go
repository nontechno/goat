package main

import (
	"os"
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

// Alt+- shows and hides it; while shown, plain keys and pastes don't reach
// the program, the cursor is hidden, and the list is drawn over the windows.
func TestEventsWindow(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 80, 24)
	w := fakeFramed(1, 0, 0, 40, 10, true, frameCompact)
	m.windows = []*Window{w}
	m.focus(w)
	m.logEvent(w, evNotify, "build done")
	m.logEvent(w, evBell, "")

	if err := m.handleKey(uv.Key{Code: '-', Mod: uv.ModAlt}); err != nil || !m.ev.shown {
		t.Fatalf("Alt+- did not show the events window (err %v)", err)
	}
	if m.ev.unseen != 0 || m.eventsIndicator() != "" {
		t.Error("unseen count not cleared when shown")
	}
	before := pendingInput(w)
	_ = m.handleKey(uv.Key{Code: 'x', Text: "x"})
	_ = m.handleKey(uv.Key{Code: uv.KeyUp})
	m.paste("rm -rf /\n")
	if pendingInput(w) != before {
		t.Error("input reached the program while the events window was shown")
	}
	if _, _, ok := m.cursorFor(); ok {
		t.Error("cursor shown under the events window")
	}
	screen := strings.Join(render(m), "\n")
	for _, want := range []string{"events (2)", "1:bash  notify  build done", "1:bash  bell"} {
		if !strings.Contains(screen, want) {
			t.Errorf("%q not on screen:\n%s", want, screen)
		}
	}

	_ = m.handleKey(uv.Key{Code: uv.KeyEscape})
	if m.ev.shown {
		t.Fatal("Esc did not close the events window")
	}
	m.logEvent(w, evBell, "x")
	if !strings.Contains(render(m)[23], "!1") {
		t.Errorf("no unseen indicator in the status bar: %q", render(m)[23])
	}
	_ = m.handleKey(uv.Key{Code: '-', Mod: uv.ModAlt})
	_ = m.handleKey(uv.Key{Code: '-', Mod: uv.ModAlt})
	if m.ev.shown {
		t.Error("Alt+- did not close the events window")
	}
}

// Scrolling stays within the list; the newest entry is shown by default.
func TestEventsWindowScroll(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 60, 12)
	for i := range 40 {
		m.logEvent(nil, evError, "entry "+string(rune('A'+i%26))+strings.Repeat(".", i))
	}
	m.toggleEvents()
	if !strings.Contains(strings.Join(render(m), "\n"), "entry N"+strings.Repeat(".", 39)[:10]) {
		t.Error("newest entry not shown")
	}
	_ = m.handleKey(uv.Key{Code: uv.KeyHome})
	top := m.ev.scroll
	if !strings.Contains(strings.Join(render(m), "\n"), "error  entry A ") {
		t.Error("oldest entry not shown after Home")
	}
	_ = m.handleKey(uv.Key{Code: uv.KeyPgUp})
	if m.ev.scroll != top {
		t.Errorf("scrolled past the oldest entry: %d > %d", m.ev.scroll, top)
	}
	_ = m.handleKey(uv.Key{Code: uv.KeyEnd})
	if m.ev.scroll != 0 {
		t.Errorf("End: scroll %d", m.ev.scroll)
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
