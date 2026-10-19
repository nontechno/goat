package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"time"
)

// The status window (F2): goat itself. What doesn't change (version, build,
// where it runs, its files) and what does (windows, processes, memory,
// CPU, history, events, program status), refreshed every second while it
// is on screen. It is a panel (panels.go) like the help window: move it,
// resize it, scroll it, copy from it; F2 shows it (focused) and F2 again
// hides it, focused or not; Alt+x hides it too.

// infoEvery is how often the status and inventory windows are refreshed.
const infoEvery = time.Second

// infoState is the status window and when it was last written.
type infoState struct {
	win    *Window
	shownW int       // the content width it was written for (0: not yet)
	at     time.Time // when
	procs  *procTable
}

// isInfo reports whether w is the status window.
func (m *WM) isInfo(w *Window) bool { return w != nil && w == m.info.win }

// infoShown reports whether the status window is on screen.
func (m *WM) infoShown() bool { return m.panelShown(m.info.win) }

// toggleInfo is F2: show the status window, or hide it.
func (m *WM) toggleInfo() {
	if m.infoShown() {
		m.hidePanel(m.info.win)
		return
	}
	if m.info.win == nil {
		m.info.win = m.newPanelWindow("status", 200, placeLeft)
	}
	m.info.shownW = 0
	m.procs.request(time.Now())
	m.showPanel(m.info.win)
}

// syncInfo rewrites the status window when it is due: shown for the first
// time, resized, a second passed, or a new process sample arrived.
func (m *WM) syncInfo(now time.Time) {
	w := m.info.win
	if !m.infoShown() || m.panelSelectionBusy(w) {
		return
	}
	procs := m.procs.table.Load()
	if w.contentW() == m.info.shownW && now.Sub(m.info.at) < infoEvery && procs == m.info.procs {
		return
	}
	first := m.info.shownW == 0
	m.info.shownW, m.info.at, m.info.procs = w.contentW(), now, procs
	m.writePanel(w, m.infoLines(w.contentW(), now), first)
}

// buildInfo describes the binary: "go1.24.7 darwin/arm64 · 3f2a9c1
// (modified) 2026-10-18 12:00".
func buildInfo() string {
	parts := []string{runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		var rev, at string
		modified := false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.time":
				if t, err := time.Parse(time.RFC3339, s.Value); err == nil {
					at = t.Local().Format("2006-01-02 15:04")
				}
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
		if rev != "" {
			if len(rev) > 7 {
				rev = rev[:7]
			}
			if modified {
				rev += " (modified)"
			}
			parts = append(parts, strings.TrimSpace(rev+" "+at))
		}
	}
	return strings.Join(parts, " · ")
}

// infoSection is a heading and its (label, value) rows.
type infoSection struct {
	title string
	rows  [][2]string
}

// infoText is what the status window shows, now.
func (m *WM) infoText(now time.Time) []infoSection {
	host, user := m.host, m.user
	if host == "" {
		host = systemHostName()
	}
	if user == "" {
		user = currentUserName()
	}
	shownHost := host
	if a := m.showHost(host); a != host {
		shownHost = a + " (" + host + ")"
	}
	term := os.Getenv("TERM")
	if id := terminalID(); id != "unknown" {
		term = id + ", " + term
	}
	since := now.Sub(m.started)
	static := infoSection{"goat " + version, [][2]string{
		{"build", buildInfo()},
		{"running", fmt.Sprintf("since %s (%s) · pid %d", m.started.Format("2006-01-02 15:04:05"), fmtSince(since), os.Getpid())},
		{"on", hostSymbol + shownHost + " as " + userSymbol + user},
		{"terminal", fmt.Sprintf("%s · %d×%d", term, m.cols, m.rows)},
		{"config", orNone(homeShort(m.cfgPath, m.home), "(defaults: no config.toml)")},
		{"local", orNone(homeShort(m.localPath, m.home), "(none)")},
		{"aliases", m.aliasInfo()},
		{"palettes", m.combosInfo()},
		{"system", orNone(homeShort(m.sys.path, m.home), "(none)") + orNone(map[bool]string{true: " · not used: " + m.sys.err}[m.sys.err != ""], "")},
		{"log", orNone(homeShort(logPath, m.home), "(off)")},
	}}

	// Windows.
	floating := m.byID()
	hidden, pinned := 0, 0
	for _, w := range floating {
		if w.minimized {
			hidden++
		}
		if w.pinned {
			pinned++
		}
	}
	var notes []string
	if hidden > 0 {
		notes = append(notes, fmt.Sprintf("%d hidden", hidden))
	}
	if pinned > 0 {
		notes = append(notes, fmt.Sprintf("%d pinned", pinned))
	}
	wins := fmt.Sprintf("%d floating", len(floating))
	if len(notes) > 0 {
		wins += " (" + strings.Join(notes, ", ") + ")"
	}
	if m.bg != nil {
		wins += " + the background"
	}
	var panels []string
	for _, w := range m.windows {
		if m.isPanel(w) {
			panels = append(panels, m.tabName(w))
		}
	}
	focused := "(none)"
	if m.focused != nil {
		focused = m.tabName(m.focused)
	}

	// Processes in the windows, goat's memory and CPU.
	procs := m.procs.table.Load()
	programs := "…"
	var goatRSS string
	switch {
	case procs == nil:
	case procs.err != "":
		programs = "unknown: " + procs.err
	default:
		var all procTotal
		for _, w := range m.programWindows() {
			if t, ok := procs.tree(w.pid()); ok {
				all.count += t.count
				all.rss += t.rss
				all.cpu += t.cpu
			}
		}
		programs = fmt.Sprintf("%s · %s memory · %s CPU", nouns(all.count, "process", "processes"), fmtBytes(all.rss), fmtPct(all.cpu))
		if p, ok := procs.procs[os.Getpid()]; ok {
			goatRSS = fmtBytes(p.rss) + " resident · "
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	memory := fmt.Sprintf("%sGo heap %s of %s from the system", goatRSS, fmtBytes(ms.HeapAlloc), fmtBytes(ms.Sys))
	cpu := "…"
	if pct, total, ok := m.cpu.read(now); ok {
		cpu = fmt.Sprintf("%s now · %s in all", fmtPct(pct), total.Round(10*time.Millisecond))
	} else if total > 0 {
		cpu = fmt.Sprintf("%s in all", total.Round(10*time.Millisecond))
	}
	history := 0
	for _, w := range m.programWindows() {
		history += w.emu.ScrollbackLen()
	}
	events := nouns(len(m.ev.list), "event", "events")
	if m.ev.unseen > 0 {
		events += fmt.Sprintf(" (%d unseen)", m.ev.unseen)
	}

	dynamic := infoSection{"Now", [][2]string{
		{"windows", wins},
		{"panels", orNone(strings.Join(panels, ", "), "(none)")},
		{"focused", focused},
		{"programs", programs},
		{"goat memory", memory},
		{"goat CPU", cpu},
		{"goroutines", fmtCount(runtime.NumGoroutine())},
		{"history", fmtCount(history) + " lines kept"},
		{"events", events},
		{"app status", m.appStatusInfo()},
	}}
	return []infoSection{static, dynamic}
}

// orNone is s, or none if s is empty.
func orNone(s, none string) string {
	if s == "" {
		return none
	}
	return s
}

// aliasInfo describes alias.toml: where, and how many aliases (or its
// mistake).
func (m *WM) aliasInfo() string {
	st := &m.alias
	if st.path == "" {
		return "(none)"
	}
	where := homeShort(st.path, m.home)
	if st.err != "" {
		return where + " · not used: " + st.err
	}
	n := 0
	for _, rules := range st.a.rules {
		n += len(rules)
	}
	if n == 0 && st.mod.IsZero() {
		return where + " (none)"
	}
	return where + " · " + nouns(n, "alias", "aliases")
}

// combosInfo describes the palettes: how many, the names' style, the color
// depth used.
func (m *WM) combosInfo() string {
	if m.combos == nil {
		return "none: " + combosFile + " missing (goat -install)"
	}
	depth := "256 colors"
	if m.combos.trueColor {
		depth = "24-bit color"
	}
	return fmt.Sprintf("%d · names: %s · %s", len(m.combos.all), comboLingos[m.lingo], depth)
}

// appStatusInfo sums up OSC 7501 reports: "2 windows: 1 working, 1 blocked".
func (m *WM) appStatusInfo() string {
	reporting := 0
	counts := map[statusState]int{}
	for _, w := range m.programWindows() {
		if len(w.status.records) > 0 {
			reporting++
		}
		for _, rec := range w.status.records {
			counts[rec.state]++
		}
	}
	if reporting == 0 {
		return "no program reports its status (OSC 7501)"
	}
	var parts []string
	for _, st := range []statusState{stBlocked, stError, stWorking, stDone, stIdle} {
		if counts[st] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[st], st))
		}
	}
	return nouns(reporting, "window", "windows") + ": " + strings.Join(parts, ", ")
}

// programWindows are the windows with programs: the background first, then
// the floating ones by number.
func (m *WM) programWindows() []*Window {
	ws := m.byID()
	if m.bg != nil {
		ws = append([]*Window{m.bg}, ws...)
	}
	return slices.DeleteFunc(ws, func(w *Window) bool { return w.closed })
}

// pid is the window's program (its shell); 0 if none.
func (w *Window) pid() int {
	if w.cmd == nil || w.cmd.Process == nil {
		return 0
	}
	return w.cmd.Process.Pid
}

// infoLines lays the status text out for a width: labels, and values
// wrapped beside them.
func (m *WM) infoLines(width int, now time.Time) []string {
	return labeledLines(m.infoText(now), width)
}

// labeledLines lays out sections of (label, value) rows: a bold heading,
// then "  label  value" with long values wrapped under themselves (or
// under the label, in a narrow window). No line is wider than width.
func labeledLines(sections []infoSection, width int) []string {
	const bold, plain = "\x1b[1m", "\x1b[0m"
	labelW := 0
	for _, s := range sections {
		for _, r := range s.rows {
			labelW = max(labelW, textWidth(r[0]))
		}
	}
	indent := 2 + labelW + 2
	twoCols := width >= indent+20
	var lines []string
	add := func(prefix, text string) {
		lines = append(lines, fitStart(prefix+text, width))
	}
	for i, s := range sections {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, bold+fitStart(s.title, width)+plain)
		for _, r := range s.rows {
			if !twoCols {
				add("  ", r[0])
				for _, v := range wrapWords(r[1], max(width-4, 8)) {
					add("    ", v)
				}
				continue
			}
			for j, v := range wrapWords(r[1], width-indent) {
				if j == 0 {
					add("  "+r[0]+strings.Repeat(" ", labelW-textWidth(r[0])+2), v)
				} else {
					add(strings.Repeat(" ", indent), v)
				}
			}
		}
	}
	return lines
}
