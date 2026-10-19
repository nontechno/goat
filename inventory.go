package main

import (
	"fmt"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// The inventory window (F3): every window, with what runs in it (the
// processes under its shell, their memory and CPU), where (directory,
// user@host, git), its size and history, and what its program reports
// (OSC 7501, status.go). Refreshed every second while on screen.
//
// One window is chosen (highlighted): ↑ ↓ (and Home/End) choose another,
// a click chooses one, Enter or a double click goes to it (focuses it,
// and brings it back if Alt+0 hid it). PageUp/PageDown scroll. It is a
// panel (panels.go): F3 shows it (focused, choosing the window that was
// active), F3 again hides it, focused or not; Alt+x hides it too.

// inventoryState is the inventory window and what it shows.
type inventoryState struct {
	win    *Window
	chosen *Window   // the highlighted entry
	rows   []invRow  // the entries as last written, in order
	shownW int       // the content width it was written for (0: not yet)
	at     time.Time // when
	procs  *procTable
	// The last click on an entry, for double clicks (the text is
	// rewritten in between, so the selection's click count can't tell).
	clickAt  time.Time
	clickWin *Window
}

// invRow is one entry's lines in the window: [first, last].
type invRow struct {
	win         *Window
	first, last int
}

// isInventory reports whether w is the inventory window.
func (m *WM) isInventory(w *Window) bool { return w != nil && w == m.inv.win }

// inventoryShown reports whether the inventory window is on screen.
func (m *WM) inventoryShown() bool { return m.panelShown(m.inv.win) }

// toggleInventory is F3: show the inventory window, choosing the active
// window, or hide it.
func (m *WM) toggleInventory() {
	if m.inventoryShown() {
		m.hidePanel(m.inv.win)
		return
	}
	if m.inv.win == nil {
		m.inv.win = m.newPanelWindow("inventory", 500, placeCenter)
	}
	m.inv.chosen = m.focused
	m.inv.shownW = 0
	m.procs.request(time.Now())
	m.showPanel(m.inv.win)
}

// inventoryEntries are the windows listed: the background, the floating
// windows by number, then the other panels on screen.
func (m *WM) inventoryEntries() []*Window {
	ws := m.programWindows()
	for _, w := range m.windows {
		if m.isPanel(w) && !m.isInventory(w) {
			ws = append(ws, w)
		}
	}
	return ws
}

// syncInventory rewrites the inventory window when it is due: shown for the
// first time, resized, a second passed, or a new process sample arrived.
func (m *WM) syncInventory(now time.Time) {
	w := m.inv.win
	if !m.inventoryShown() || m.panelSelectionBusy(w) {
		return
	}
	procs := m.procs.table.Load()
	if w.contentW() == m.inv.shownW && now.Sub(m.inv.at) < infoEvery && procs == m.inv.procs {
		return
	}
	m.writeInventory(now)
}

// writeInventory writes the list now.
func (m *WM) writeInventory(now time.Time) {
	w := m.inv.win
	first := m.inv.shownW == 0
	m.inv.shownW, m.inv.at, m.inv.procs = w.contentW(), now, m.procs.table.Load()
	lines, rows := m.inventoryLines(w.contentW())
	m.inv.rows = rows
	m.writePanel(w, lines, first)
	if first {
		m.showChosen()
	}
}

// inventoryLines lays the list out for a width, and says which lines are
// whose.
func (m *WM) inventoryLines(width int) ([]string, []invRow) {
	const bold, dim, reverse, plain = "\x1b[1m", "\x1b[2m", "\x1b[7m", "\x1b[0m"
	entries := m.inventoryEntries()
	if !slices.Contains(entries, m.inv.chosen) && len(entries) > 0 {
		m.inv.chosen = entries[0]
	}
	floating := len(m.byID())
	lines := []string{
		bold + fitStart(fmt.Sprintf("goat: %s", nouns(floating, "floating window", "floating windows"))+
			map[bool]string{true: " + the background", false: ""}[m.bg != nil], width) + plain,
		dim + fitStart("↑ ↓ choose · Enter go to it · F3 close", width) + plain,
		"",
	}
	var rows []invRow
	procs := m.procs.table.Load()
	track := m.meterTrack(m.inv.win)
	for _, e := range entries {
		row := invRow{win: e, first: len(lines)}
		head := m.tabName(e)
		if flags := m.inventoryFlags(e); flags != "" {
			head += "   " + flags
		}
		head = fitStart(head, width)
		if e == m.inv.chosen {
			head = reverse + head + strings.Repeat(" ", max(width-textWidth(head), 0)) + plain
		} else {
			head = bold + head + plain
		}
		lines = append(lines, head)
		for _, d := range m.inventoryDetails(e, procs, width-4, track) {
			if raw, ok := strings.CutPrefix(d, rawLine); ok { // laid out already (meters)
				lines = append(lines, fitWide("    "+raw, width))
				continue
			}
			for _, part := range wrapWords(d, max(width-6, 10)) {
				lines = append(lines, fitStart("    "+part, width))
			}
		}
		row.last = len(lines) - 1
		rows = append(rows, row)
		lines = append(lines, "")
	}
	return lines, rows
}

// inventoryFlags is a window's state: "background · pinned · 👻 hidden".
func (m *WM) inventoryFlags(w *Window) string {
	var f []string
	switch {
	case w.background:
		f = append(f, "background")
	case m.isPanel(w):
		f = append(f, "goat")
	}
	if w.pinned {
		f = append(f, "pinned")
	}
	if w.minimized {
		f = append(f, ghost+" hidden")
	}
	if w.held || w.exited.Load() {
		f = append(f, "ended")
	}
	if w.scroll > 0 && !m.isPanel(w) {
		f = append(f, "scrolled back")
	}
	return strings.Join(f, " · ")
}

// rawLine starts a detail line that is laid out already (it has bars,
// whose spaces mustn't be wrapped at).
const rawLine = "\x00"

// inventoryDetails are the lines under a window's name, for a width.
func (m *WM) inventoryDetails(w *Window, procs *procTable, width int, track string) []string {
	if m.isPanel(w) {
		what := map[string]string{
			"events":    "bells, notifications, exits, … (Alt+" + m.cfg.Keys.ShowEvents + ")",
			"help":      "keys and mouse (F1)",
			"status":    "goat itself (F2)",
			"inventory": "every window (F3)",
		}[m.panelName(w)]
		return []string{what}
	}
	var out []string

	// What runs in it.
	run := w.procName
	if run == "" {
		run = w.shellName
	}
	switch t, ok := procs.tree(w.pid()); {
	case w.pid() == 0:
		out = append(out, orNone(run, "no program"))
	case procs == nil:
		out = append(out, fmt.Sprintf("%s · pid %d · …", run, w.pid()))
	case !ok:
		out = append(out, fmt.Sprintf("%s · pid %d", run, w.pid()))
	default:
		names := t.names
		if len(names) > 4 {
			names = append(slices.Clone(names[:4]), ellipsis)
		}
		out = append(out, fmt.Sprintf("%s · %s · pid %d",
			strings.Join(names, " "), nouns(t.count, "process", "processes"), w.pid()))
		out = append(out, inventoryMeters(t, width, track)...)
	}

	// Where.
	var where []string
	if w.dir != "" {
		where = append(where, m.showDir(w.dir))
	}
	if l := m.frameLabel(w); l != "" {
		where = append(where, l)
	}
	where = append(where, fmt.Sprintf("%d×%d", w.contentW(), w.contentH()),
		nouns(w.emu.ScrollbackLen(), "line", "lines")+" of history")
	if w.nowrap {
		where = append(where, "not wrapping")
	}
	if w.pal > 0 { // (Alt+t)
		where = append(where, "palette "+m.comboName(w.pal-1))
	}
	out = append(out, strings.Join(where, " · "))

	// What its program reports (OSC 7501).
	ids := make([]string, 0, len(w.status.records))
	for id := range w.status.records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	const most = 5
	for i, id := range ids {
		if i == most {
			out = append(out, fmt.Sprintf("status: %d more", len(ids)-most))
			break
		}
		r := w.status.records[id].statusReport
		out = append(out, "status: "+statusShortText(id, r, w.status.app(id)))
		if r.progress >= 0 { // its progress as a bar too
			pct := fmt.Sprintf("%d%%", r.progress)
			out = append(out, rawLine+"        "+meterBar(float64(r.progress)/100, meterWidth(width-8-len(pct)-1), track)+" "+pct)
		}
	}
	return out
}

// inventoryMeters are a window's CPU and memory, with bars (meter.go):
// on one line when they fit, else one each.
//
//	CPU    █▍░░░░░░░░░░ 1.4% of 8 CPUs   memory ██▌░░░░░░░░░ 2.1 GB of 16.0 GB
func inventoryMeters(t procTotal, width int, track string) []string {
	cpus := float64(runtime.NumCPU())
	ram := physicalMemory()
	type meter struct {
		label, text string
		frac        float64
	}
	ms := []meter{
		{"CPU", cpuText(t.cpu, cpus), share(t.cpu, 100*cpus)},
		{"memory", memText(t.rss, ram), share(float64(t.rss), float64(ram))},
	}
	if ram == 0 { // no total to measure against: the number alone
		ms[1].frac = -1
	}
	draw := func(m meter, bw int) string {
		s := fmt.Sprintf("%-6s ", m.label)
		if m.frac >= 0 {
			s += meterBar(m.frac, bw, track) + " "
		}
		return s + m.text
	}
	const gap = "   "
	textW := 0
	for _, m := range ms {
		textW = max(textW, textWidth(m.text))
	}
	// One line: two bars and their texts.
	if bw := (width - len(gap) - 2*(7+1+textW)) / 2; bw >= 8 {
		bw = min(bw, 12)
		return []string{rawLine + draw(ms[0], bw) + strings.Repeat(" ", max(textW-textWidth(ms[0].text), 0)) + gap + draw(ms[1], bw)}
	}
	bw := meterWidth(width - 7 - textW - 1)
	return []string{rawLine + draw(ms[0], bw), rawLine + draw(ms[1], bw)}
}

// statusShortText is a status record in a few words:
// "deploy/eu: blocked (permission) 40% claude: Deploy? — msg".
func statusShortText(id string, r statusReport, app string) string {
	s := string(r.state)
	if id != "" {
		s = id + ": " + s
	}
	if r.kind != "" {
		s += " (" + r.kind + ")"
	}
	if r.progress >= 0 {
		s += fmt.Sprintf(" %d%%", r.progress)
	}
	if app != "" {
		s += " · " + app
	}
	text := r.title
	if r.msg != "" {
		if text != "" {
			text += " — "
		}
		text += r.msg
	}
	if text != "" {
		s += " · " + text
	}
	return s
}

// inventoryKey handles the inventory's own keys; false for the others
// (which scroll it).
func (m *WM) inventoryKey(k uv.Key) bool {
	if k.Mod != 0 {
		return false
	}
	rows := m.inv.rows
	i := slices.IndexFunc(rows, func(r invRow) bool { return r.win == m.inv.chosen })
	switch k.Code {
	case uv.KeyUp:
		m.choose(max(i-1, 0))
	case uv.KeyDown:
		m.choose(min(i+1, len(rows)-1))
	case uv.KeyHome:
		m.choose(0)
	case uv.KeyEnd:
		m.choose(len(rows) - 1)
	case uv.KeyEnter:
		m.goToChosen()
	default:
		return false
	}
	return true
}

// choose highlights entry i and scrolls it into view.
func (m *WM) choose(i int) {
	if i < 0 || i >= len(m.inv.rows) {
		return
	}
	if m.inv.chosen != m.inv.rows[i].win {
		m.inv.chosen = m.inv.rows[i].win
		m.writeInventory(time.Now())
	}
	m.showChosen()
}

// showChosen scrolls the chosen entry into view.
func (m *WM) showChosen() {
	for _, r := range m.inv.rows {
		if r.win == m.inv.chosen {
			showPanelLines(m.inv.win, r.first, r.last)
			m.dirty = true
		}
	}
}

// goToChosen is Enter: focus the chosen window (bringing it back if
// hidden). The inventory stays open, under it.
func (m *WM) goToChosen() {
	w := m.inv.chosen
	if w == nil || !slices.Contains(m.windows, w) || w.closed {
		return
	}
	m.focus(w)
}

// inventoryClick chooses the entry under a click (line: absolute, as
// posAt has it); a second click on it soon after goes to it.
func (m *WM) inventoryClick(line int) {
	w := m.inv.win
	n := panelLine(w, line)
	now := time.Now()
	for i, r := range m.inv.rows {
		if n < r.first || n > r.last {
			continue
		}
		double := m.inv.clickWin == r.win && now.Sub(m.inv.clickAt) < multiClickTime
		m.inv.clickAt, m.inv.clickWin = now, r.win
		m.choose(i)
		if double {
			m.inv.clickWin = nil
			m.clearSelectionIn(w) // a double click here isn't a word to copy
			m.drag = drag{}
			m.goToChosen()
		}
		return
	}
}
