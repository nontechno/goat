package main

import (
	"io"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// hookedWindow is a window with goat's sequence hooks, as newWindow sets
// it up, and replies: what went back to its program since the last call.
// Replies go out in order, so replies asks for the operating status (DSR
// 5) last and reads up to its answer: no guessing how long to wait.
func hookedWindow(t *testing.T, m *WM) (*Window, func() string) {
	t.Helper()
	w := fakeWindow(7, 0, 0, 42, 12, true, false)
	w.mouseModes = map[ansi.DECMode]bool{}
	w.installHooks(m.cellSize)
	m.hookEvents(w)
	m.windows = append(m.windows, w)
	ch := make(chan string, 64)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := w.emu.Read(buf)
			if err != nil {
				close(ch)
				return
			}
			ch <- string(buf[:n])
		}
	}()
	t.Cleanup(func() { // (as Window.close: emu.Close races with Read)
		if pw, ok := w.emu.InputPipe().(*io.PipeWriter); ok {
			_ = pw.CloseWithError(io.EOF)
		}
	})
	marker := ansi.DeviceStatusReport(ansi.DECStatusReport(0)) // the emulator's answer to DSR 5: "OK"
	replies := func() string {
		t.Helper()
		_ = w.feed([]byte("\x1b[5n")) // (its reader takes the answer: ch has room)
		var b strings.Builder
		deadline := time.After(10 * time.Second)
		for {
			select {
			case s, ok := <-ch:
				if !ok {
					return b.String()
				}
				b.WriteString(s)
				if got, done := strings.CutSuffix(b.String(), marker); done {
					return got
				}
			case <-deadline:
				t.Fatalf("no answer to DSR 5; so far %q", b.String())
			}
		}
	}
	return w, replies
}

// Claude Code's startup burst (claude-server.md, section 3), fed a byte
// at a time: the replies, in the order asked.
func TestClaudeStartupBurst(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	m.cellW, m.cellH = 9, 18
	w, replies := hookedWindow(t, m)
	burst := "\x1b[>0q\x1b[?u\x1b]7501;?\x07\x1b[c\x1b[<u\x1b[>5u\x1b[>4;2m\x1b[?2026$p" +
		"\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\\x1b[16t\x1b[?1016$p\x1b[c"
	for i := 0; i < len(burst); i++ {
		_ = w.feed([]byte{burst[i]})
	}
	got := replies()
	want := []string{
		"\x1bP>|goat " + version + "\x1b\\",
		"\x1b]7501;?:states=idle,working,done,blocked,error,clear:kinds=permission,question,auth\x1b\\",
		"\x1b[?62;", // DA1
		"\x1b[?2026;0$y",
		"\x1b[6;18;9t",
		"\x1b[?1016;0$y",
		"\x1b[?62;", // DA1 again
	}
	at := 0
	for _, s := range want {
		i := strings.Index(got[at:], s)
		if i < 0 {
			t.Fatalf("missing or out of order: %q\nin %q", s, got)
		}
		at += i + len(s)
	}
	// The 7501 reply comes before the first DA1 reply.
	if strings.Index(got, "\x1b]7501") > strings.Index(got, "\x1b[?62;") {
		t.Errorf("DA1 before the 7501 reply: %q", got)
	}
	// Nothing for the graphics probe.
	if strings.Contains(got, "\x1b_G") {
		t.Errorf("graphics reply: %q", got)
	}
}

// Sizes: in pixels only when goat's terminal said; in characters always.
func TestWindowSizeReplies(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	w, replies := hookedWindow(t, m)
	_ = w.feed([]byte("\x1b[16t\x1b[14t\x1b[18t"))
	got := replies()
	if got != "\x1b[8;"+itoa(w.emu.Height())+";"+itoa(w.emu.Width())+"t" {
		t.Errorf("unknown cell size: %q", got)
	}
	m.handleEvent(uv.CellSizeEvent{Width: 10, Height: 20}, nil)
	_ = w.feed([]byte("\x1b[14t"))
	if got := replies(); got != "\x1b[4;"+itoa(20*w.emu.Height())+";"+itoa(10*w.emu.Width())+"t" {
		t.Errorf("text area: %q", got)
	}
}

// Mode 2031: the program is told dark or light when it asks, when it
// sets the mode, and when the window's background changes.
func TestColorSchemeReports(t *testing.T) {
	m, _, _, _ := paletteWM(t)
	w, replies := hookedWindow(t, m)
	m.hostBg = ansi.IndexedColor(16)   // a black terminal
	_ = w.feed([]byte("\x1b[?2031$p")) // has goat the mode? yes, reset
	if got := replies(); got != "\x1b[?2031;2$y" {
		t.Fatalf("DECRQM before: %q", got)
	}
	_ = w.feed([]byte("\x1b[?996n"))
	if got := replies(); got != "\x1b[?997;1n" {
		t.Fatalf("query: %q", got)
	}
	_ = w.feed([]byte("\x1b[?2031h"))
	if got := replies(); got != "\x1b[?997;1n" {
		t.Fatalf("on: %q", got)
	}
	m.setCombo(w, m.combos.byTheme[2][0]) // a light palette
	if got := replies(); got != "\x1b[?997;2n" {
		t.Fatalf("light palette: %q", got)
	}
	m.setCombo(w, m.combos.byTheme[2][5]) // light again: nothing new
	m.setHostColors(nil, ansi.IndexedColor(231))
	if got := replies(); got != "" {
		t.Fatalf("no change, yet: %q", got)
	}
	m.setCombo(w, -1) // the terminal's colors: now white
	if got := replies(); got != "" {
		t.Fatalf("still light: %q", got)
	}
	m.setHostColors(nil, ansi.IndexedColor(16)) // the terminal turned dark
	if got := replies(); got != "\x1b[?997;1n" {
		t.Fatalf("terminal dark: %q", got)
	}
	_ = w.feed([]byte("\x1b[?2031$p"))
	if got := replies(); got != "\x1b[?2031;1$y" {
		t.Fatalf("DECRQM set: %q", got)
	}
	_ = w.feed([]byte("\x1b[?2031l"))
	m.setHostColors(nil, ansi.IndexedColor(231))
	if got := replies(); got != "" {
		t.Fatalf("mode off, yet: %q", got)
	}
	_ = w.feed([]byte("\x1b[?2031$p\x1b[?2026$p")) // (and another mode: the emulator's answer)
	if got := replies(); !strings.HasPrefix(got, "\x1b[?2031;2$y\x1b[?2026;") {
		t.Fatalf("DECRQM reset: %q", got)
	}
}

// Kitty notifications in pieces make one event (Claude Code's form).
func TestKittyNotificationPieces(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	w, _ := hookedWindow(t, m)
	before := len(m.ev.list)
	_ = w.feed([]byte("\x1b]99;i=2325:d=0:p=title;Claude Code\x07" +
		"\x1b]99;i=2325:p=body;Claude needs your permission\x07" +
		"\x1b]99;i=2325:d=1:a=focus;\x07"))
	evs := m.ev.list[before:]
	if len(evs) != 1 || evs[0].kind != evNotify || evs[0].text != "Claude Code: Claude needs your permission" {
		t.Fatalf("events: %+v", evs)
	}
	now := time.Now()
	for _, tc := range []struct {
		osc  string
		want string
		ok   bool
	}{
		{"99;;hello", "hello", true},                        // one piece, no id
		{"99;i=a:d=0;Title", "", false},                     // more to come
		{"99;i=a:p=body:e=1;Ym9keQ==", "Title: body", true}, // base64 body ends it
		{"99;i=a;", "", false},                              // nothing new
		{"99;i=b:p=?;", "", false},                          // a query
		{"99;i=c:p=body;only body", "only body", true},      // no title
	} {
		got, ok := w.kittyNotification([]byte(tc.osc), now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%q: %q %v, want %q %v", tc.osc, got, ok, tc.want, tc.ok)
		}
	}
}

func itoa(n int) string {
	return strings.TrimSpace(strings.ReplaceAll(fmtCount(n), ",", ""))
}
