package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestInfoToggle(t *testing.T) {
	m, bg, a, b := wmWithBackground(t)
	m.cols, m.rows = 100, 40
	withPid(bg, 100)
	fakeProcs(m,
		procSample{pid: 100, ppid: 1, rss: 2 << 20, cpu: 1, comm: "zsh"},
		procSample{pid: 101, ppid: 100, rss: 8 << 20, cpu: 3, comm: "top"},
		procSample{pid: os.Getpid(), ppid: 1, rss: 30 << 20, comm: "goat"},
	)
	m.focus(b)
	key(m, uv.Key{Code: uv.KeyF2})
	w := m.info.win
	if !m.infoShown() || m.focused != w || m.tabName(w) != "status" {
		t.Fatal("F2 didn't show the status window, focused")
	}
	text := panelScreen(m, w) + strings.Join(m.infoLines(w.contentW(), time.Now()), "\n") // (all of it: the window may be shorter)
	for _, want := range []string{"goat " + version, "build", "go1.", "pid", "Now",
		"2 floating + the background", "panels", "status", "2 processes in the windows",
		"programs CPU     " + meterBar(share(4, 100*float64(runtime.NumCPU())), 25, m.meterTrack(w)),
		"programs memory  ", "10.0 MB of ", "goat memory      ", "30.0 MB of ",
		"Go heap          ", "goroutines", "no program reports"} {
		if !strings.Contains(text, want) {
			t.Errorf("status lacks %q:\n%s", want, text)
		}
	}
	for _, label := range []string{"programs memory", "goat memory", "Go heap"} { // with bars
		for _, l := range m.infoLines(w.contentW(), time.Now()) {
			if strings.Contains(l, label) && !strings.Contains(l, m.meterTrack(w)) {
				t.Errorf("%s: no bar: %q", label, l)
			}
		}
	}
	// Not numbered, not hidden by Alt+0, can't be swapped.
	if m.number(a) != 1 || m.number(b) != 2 {
		t.Error("the status window is numbered")
	}
	key(m, uv.Key{Code: 's', Mod: uv.ModAlt})
	if m.bg != bg {
		t.Error("swapped with the background")
	}
	m.focus(bg)
	key(m, uv.Key{Code: '0', Mod: uv.ModAlt})
	if w.minimized || !m.infoShown() || !a.minimized {
		t.Error("Alt+0 hid the status window, or not the others")
	}
	key(m, uv.Key{Code: '0', Mod: uv.ModAlt})

	// Refreshed: a hidden window and an app status show a second later.
	b.minimized = true
	b.status.apply(statusReport{state: stBlocked, kind: "permission", progress: -1})
	m.tick(time.Now().Add(2 * infoEvery))
	text = strings.Join(m.infoLines(w.contentW(), time.Now()), "\n")
	if !strings.Contains(text, "(1 hidden)") || !strings.Contains(text, "1 window: 1 blocked") {
		t.Errorf("not refreshed:\n%s", text)
	}
	b.minimized = false

	// F2 again hides it, focused or not.
	m.focus(a)
	key(m, uv.Key{Code: uv.KeyF2})
	if m.infoShown() || m.focused != a {
		t.Fatal("F2 again didn't hide it")
	}
	key(m, uv.Key{Code: uv.KeyF2})
	key(m, uv.Key{Code: 'x', Mod: uv.ModAlt})
	if m.infoShown() {
		t.Fatal("Alt+x didn't hide it")
	}
}

// Narrow: labels above values; nothing wider than the window.
func TestInfoNarrow(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	m.cols, m.rows = 100, 40
	fakeProcs(m)
	m.toggleInfo()
	w := m.info.win
	w.w = 24
	w.refit()
	text := panelScreen(m, w)
	for _, line := range strings.Split(text, "\n") {
		if textWidth(line) > w.contentW() {
			t.Errorf("line wider than the window: %q", line)
		}
	}
	all := strings.Join(m.infoLines(w.contentW(), time.Now()), "\n")
	if !strings.Contains(all, "\n  windows\n    ") {
		t.Errorf("narrow layout:\n%s", all)
	}
	for _, line := range strings.Split(all, "\n") {
		if ansi.StringWidth(line) > w.contentW() { // (bars hold escape sequences)
			t.Errorf("line wider than the window: %q", line)
		}
	}
}

// Sampling runs in the background and only while a window shows it.
func TestProcSamplerRequest(t *testing.T) {
	var s procSampler
	calls := 0
	done := make(chan struct{}, 4)
	s.sample = func() procTable {
		calls++
		done <- struct{}{}
		return procTable{procs: map[int]procSample{1: {pid: 1}}}
	}
	now := time.Now()
	s.request(now)
	<-done
	for s.running.Load() {
		time.Sleep(time.Millisecond)
	}
	if s.table.Load() == nil {
		t.Fatal("no table")
	}
	s.request(now.Add(time.Second)) // recent: no new sample
	s.request(time.Now().Add(procEvery + time.Second))
	<-done
	if calls != 2 {
		t.Errorf("%d samples", calls)
	}
}
