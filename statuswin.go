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

// The status window (Alt+F2): goat itself. What doesn't change (version, build,
// where it runs, its files) and what does (windows, processes, memory,
// CPU, history, events, program status), refreshed every second while it
// is on screen. It is a panel (panels.go) like the help window: move it,
// resize it, scroll it, copy from it; Alt+F2 shows it (focused) and Alt+F2 again
// hides it, focused or not; Alt+x hides it too.
//
// "Status" is three things in goat, each with its own names:
//   - the status window, this file: statusWin…, m.statWin;
//   - what programs report about themselves (OSC 7501), appstatus.go:
//     w.appStatus, statusReport, onStatus…;
//   - the status bar at the bottom and its messages, render.go:
//     setBarMessage, m.barMsg.

// panelEvery is how often the status and inventory windows are refreshed.
const panelEvery = time.Second

// statusWinState is the status window and when it was last written.
type statusWinState struct {
	win    *Window
	shownW int       // the content width it was written for (0: not yet)
	at     time.Time // when
	procs  *procTable
}

// isStatusWin reports whether w is the status window.
func (m *WM) isStatusWin(w *Window) bool { return w != nil && w == m.statWin.win }

// statusWinShown reports whether the status window is on screen.
func (m *WM) statusWinShown() bool { return m.panelShown(m.statWin.win) }

// toggleStatusWin is Alt+F2: show the status window, or hide it.
func (m *WM) toggleStatusWin() {
	if m.statusWinShown() {
		m.hidePanel(m.statWin.win)
		return
	}
	if m.statWin.win == nil {
		m.statWin.win = m.newPanelWindow("status", 200, placeLeft)
	}
	m.statWin.shownW = 0
	m.procs.request(time.Now())
	m.showPanel(m.statWin.win)
}

// syncStatusWin rewrites the status window when it is due: shown for the first
// time, resized, a second passed, or a new process sample arrived.
func (m *WM) syncStatusWin(now time.Time) {
	w := m.statWin.win
	if !m.statusWinShown() || m.panelSelectionBusy(w, m.statWin.shownW) {
		return
	}
	procs := m.procs.table.Load()
	if w.contentW() == m.statWin.shownW && now.Sub(m.statWin.at) < panelEvery && procs == m.statWin.procs {
		return
	}
	first := m.statWin.shownW == 0
	m.statWin.shownW, m.statWin.at, m.statWin.procs = w.contentW(), now, procs
	m.writePanel(w, m.statusWinLines(w.contentW(), now), first)
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
	rows  []infoRow
}

// infoRow is a label and its value; a meter row also draws the value as a
// bar (meter.go), before its text.
type infoRow struct {
	label, value string
	frac         float64 // the bar's fill, 0 to 1
	meter        bool
}

func row(label, value string) infoRow { return infoRow{label: label, value: value} }

func meterRow(label string, frac float64, value string) infoRow {
	return infoRow{label: label, value: value, frac: frac, meter: true}
}

// cpuText is CPU use as a share of the whole machine: "0.5% of 8 CPUs"
// (pct is of one CPU, as ps and /proc have it).
func cpuText(pct, cpus float64) string {
	if cpus <= 1 {
		return fmtPct(pct)
	}
	return fmt.Sprintf("%s of %d CPUs", fmtPct(pct/cpus), int(cpus))
}

// memText is memory used, and of how much: "41.2 MB of 16.0 GB".
func memText(used, total uint64) string {
	if total == 0 {
		return fmtBytes(used)
	}
	return fmtBytes(used) + " of " + fmtBytes(total)
}

// statusWinText is what the status window shows, now.
func (m *WM) statusWinText(now time.Time) []infoSection {
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
	static := infoSection{"goat " + version, []infoRow{
		row("build", buildInfo()),
		row("running", fmt.Sprintf("since %s (%s) · pid %d", m.started.Format("2006-01-02 15:04:05"), fmtSince(since), os.Getpid())),
		row("on", hostSymbol+shownHost+" as "+userSymbol+user),
		row("terminal", fmt.Sprintf("%s · %d×%d", term, m.cols, m.rows)),
		row("colors", m.colorsInfo()),
		row("config", orNone(homeShort(m.cfgPath, m.home), "(defaults: no config.toml)")),
		row("local", orNone(homeShort(m.localPath, m.home), "(none)")),
		row("aliases", m.aliasInfo()),
		row("palettes", m.combosInfo()),
		row("system", orNone(homeShort(m.sys.path, m.home), "(none)")+orNone(map[bool]string{true: " · not used: " + m.sys.err}[m.sys.err != ""], "")),
		row("log", orNone(homeShort(logPath, m.home), "(off)")),
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

	// Processes in the windows, goat's memory and CPU: with a bar each,
	// of all the machine's CPUs and memory (meter.go).
	cpus := float64(runtime.NumCPU())
	ram := physicalMemory()
	procs := m.procs.table.Load()
	programs := row("programs", "…")
	progCPU, progMem := row("programs CPU", "…"), row("programs memory", "…")
	goatMem := row("goat memory", "…")
	switch {
	case procs == nil:
	case procs.err != "":
		programs.value = "unknown: " + procs.err
		progCPU.value, progMem.value, goatMem.value = "unknown", "unknown", "unknown"
	default:
		var all procTotal
		for _, w := range m.programWindows() {
			if t, ok := procs.tree(w.pid()); ok {
				all.count += t.count
				all.rss += t.rss
				all.cpu += t.cpu
			}
		}
		programs.value = nouns(all.count, "process", "processes") + " in the windows"
		progCPU = meterRow("programs CPU", share(all.cpu, 100*cpus), cpuText(all.cpu, cpus))
		progMem = meterRow("programs memory", share(float64(all.rss), float64(ram)), memText(all.rss, ram))
		if p, ok := procs.procs[os.Getpid()]; ok {
			goatMem = meterRow("goat memory", share(float64(p.rss), float64(ram)), memText(p.rss, ram))
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	heap := meterRow("Go heap", share(float64(ms.HeapAlloc), float64(ms.Sys)),
		fmt.Sprintf("%s of %s", fmtBytes(ms.HeapAlloc), fmtBytes(ms.Sys)))
	goatCPU := row("goat CPU", "…")
	if pct, total, ok := m.cpu.read(now); ok {
		goatCPU = meterRow("goat CPU", share(pct, 100*cpus),
			cpuText(pct, cpus)+" · "+total.Round(10*time.Millisecond).String()+" in all")
	} else if total > 0 {
		goatCPU.value = total.Round(10*time.Millisecond).String() + " in all"
	}
	history := 0
	for _, w := range m.programWindows() {
		history += w.emu.ScrollbackLen()
	}
	events := nouns(len(m.ev.list), "event", "events")
	if m.ev.unseen > 0 {
		events += fmt.Sprintf(" (%d unseen)", m.ev.unseen)
	}

	dynamic := infoSection{"Now", []infoRow{
		row("windows", wins),
		row("panels", orNone(strings.Join(panels, ", "), "(none)")),
		row("focused", focused),
		programs, progCPU, progMem,
		goatCPU, goatMem, heap,
		row("goroutines", fmtCount(runtime.NumGoroutine())),
		row("history", fmtCount(history)+" lines kept"),
		row("events", events),
		row("app status", m.appStatusInfo()),
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

// colorsInfo says whether goat passes 24-bit color on, and why.
func (m *WM) colorsInfo() string {
	if m.trueColor {
		return "24-bit (" + m.trueColorWhy + ")"
	}
	return "256: " + m.trueColorWhy
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
		if len(w.appStatus.records) > 0 {
			reporting++
		}
		for _, rec := range w.appStatus.records {
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

// statusWinLines lays the status text out for a width: labels, and values
// wrapped beside them.
func (m *WM) statusWinLines(width int, now time.Time) []string {
	return labeledLines(m.statusWinText(now), width, m.meterTrack(m.statWin.win))
}

// labeledLines lays out sections of rows: a bold heading, then
// "  label  value" with long values wrapped under themselves (or under the
// label, in a narrow window). A meter row starts its value with its bar,
// its track drawn with track.
// No line is wider than width.
func labeledLines(sections []infoSection, width int, track string) []string {
	const bold, plain = "\x1b[1m", "\x1b[0m"
	labelW := 0
	for _, s := range sections {
		for _, r := range s.rows {
			labelW = max(labelW, textWidth(r.label))
		}
	}
	indent := 2 + labelW + 2
	twoCols := width >= indent+20
	var lines []string
	add := func(prefix, text string) {
		lines = append(lines, fitWide(prefix+text, width))
	}
	// value adds a value, wrapped to the room after at; first is what
	// goes before its first line (the label), at its width.
	value := func(r infoRow, first string, at int) {
		r.value = printable(r.value) // (it may come from a program)
		room := width - at
		pad := strings.Repeat(" ", at)
		if r.meter {
			bw := meterWidth(room - textWidth(r.value) - 1) // leaving room for the number
			bar := meterBar(r.frac, bw, track) + " "
			at += bw + 1
			room = width - at
			parts := wrapWords(r.value, max(room, 8))
			if len(parts) == 0 {
				parts = []string{""}
			}
			add(first, bar+parts[0])
			for _, p := range parts[1:] {
				add(strings.Repeat(" ", at), p)
			}
			return
		}
		for j, v := range wrapWords(r.value, max(room, 8)) {
			if j == 0 {
				add(first, v)
			} else {
				add(pad, v)
			}
		}
	}
	for i, s := range sections {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, bold+fitStart(s.title, width)+plain)
		for _, r := range s.rows {
			if !twoCols {
				add("  ", r.label)
				value(r, "    ", 4)
				continue
			}
			value(r, "  "+r.label+strings.Repeat(" ", labelW-textWidth(r.label)+2), indent)
		}
	}
	return lines
}
