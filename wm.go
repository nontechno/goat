package main

import (
	"fmt"
	"image/color"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// dragKind is the mouse operation in progress.
type dragKind int

const (
	dragNone    dragKind = iota
	dragMove             // title bar
	dragLeft             // left edge
	dragRight            // right edge
	dragBottom           // bottom edge
	dragBottomL          // bottom-left corner
	dragBottomR          // bottom-right corner
	dragContent          // button held inside a program that wants the mouse
	dragSelect           // selecting text
)

type drag struct {
	kind    dragKind
	win     *Window
	offX    int // pointer offset from window origin (move)
	offY    int
	anchorR int // right edge x+w, fixed while dragging a left side
	// Pointer and window size when a resize started: sizes follow the
	// pointer's movement from there.
	startX, startY, startW, startH int
}

// WM owns all windows and UI state. It is used only from the event loop.
type WM struct {
	cfg *Config
	// contrastCache remembers border colors adjusted for window
	// backgrounds (visibleOn).
	contrastCache map[contrastKey]color.Color
	warnings      []string
	cols, rows    int
	macOption     bool // treat macOS Option characters as Alt+key (mac_option_keys)
	optionArrows  bool // Esc b / Esc f are Option+Left / Right (mac_option_arrows)
	arrowsLearn   bool // still finding out which (mac_option_arrows = "auto")
	// From the terminal's profile learned by goat -learn (local.toml):
	keyRemap    map[string]uv.Key // keys the terminal sends differently
	plainIdent  bool              // frames show user@host (no 2-column emoji)
	profiled    bool              // a profile is in use
	home        string            // $HOME, shown as ~ in the status bar
	host        string            // shown in window frames (show_host); "" = none
	user        string            // goat's own user name (show_user)
	identOn     bool              // poll window identities (show_user/show_host; set at start)
	identOut    chan identMsg
	gitOut      chan gitMsg // background git lookups (git.go)
	userNames   map[int]string
	hostChecked time.Time
	// The right end of the status bar (local.toml show_clock,
	// show_launch_host), re-read every few seconds.
	bar        barRight
	barChecked time.Time

	windows []*Window // z-order, bottom first; pinned windows sit on top
	focused *Window
	bg      *Window // background shell, always windows[0]
	dir     string  // launch directory, where the background shell starts
	nextID  int
	out     chan ptyMsg

	drag drag

	escPending bool
	escTimer   *time.Timer
	escFire    chan struct{}

	pasting  bool
	pasteBuf []byte

	hostFg, hostBg color.Color // host terminal default colors, once known
	combos         *comboSet   // palettes (combos.go; nil: none)
	// keymap.toml (keymap.go), and the keys config.toml gave (what it
	// saves differences from).
	keymapPath string
	keysBase   KeyConfig
	// 24-bit color to the terminal and its programs, and why (truecolor.go).
	trueColor    bool
	trueColorWhy string
	// A character cell in pixels, as the terminal told goat (0: not
	// known); programs asking get it (termreplies.go).
	cellW, cellH int
	lingo        int // the palette names' style (comboLingos)
	// local.toml, and the background window's colors saved there (desk;
	// deskKnown: there are some). No localPath: nothing is saved.
	localPath string
	desk      desktopColors
	deskKnown bool

	sel          *selection             // current text selection, if any
	clicks       int                    // 1, 2, 3 for single/double/triple click
	lastClick    time.Time              // for multi-click detection
	lastClickPos selPos                 //
	lastClickWin *Window                //
	copy         func(text, who string) // puts text on the clipboard; who: "" or a program

	ev events // the events window (events.go)

	barMsg      string          // a message in the status bar (setBarMessage)
	barMsgUntil time.Time       // when it disappears
	tabs        []tab           // status bar tab positions, for mouse clicks
	quitAsk     *quitDialog     // the "Quit goat?" dialog, while it is up
	settings    *settingsDialog // the settings dialog (Alt+o), while it is up
	// config.toml's wrap_lines, before local.toml's (the settings dialog's
	// default); nil if local.toml didn't change it.
	wrapFromConfig *bool
	lastMinute     int // clock redraw

	// Settings from local.toml (localopts.go): new window size, confirm
	// quit, exit on last window.
	local localOpts
	// Aliases from alias.toml (alias.go), re-read when the file changes.
	alias aliasState
	// The help window (Alt+F1; help.go), the status window (Alt+F2; statuswin.go) and
	// the inventory (Alt+F3; inventory.go).
	help    helpState
	statWin statusWinState
	inv     inventoryState
	// system.toml: their titles, keys, palettes, sizes and places
	// (system.go).
	sys systemState
	// The palette dialog (Alt+t; paldlg.go), while it is up.
	palDlg *paletteDialog
	// Process samples for the status and inventory windows (procinfo.go),
	// and goat's own CPU use.
	procs procSampler
	cpu   cpuMeter
	// When goat started, and the config.toml it read ("" if none).
	started time.Time
	cfgPath string

	dirty bool
	quit  bool

	// The host screen is to be repainted whole with the next frame
	// (repaint), and when it last was.
	repaintHost bool
	lastRepaint time.Time
}

// tab is a window's label in the status bar.
type tab struct {
	x0, x1 int // columns [x0, x1)
	win    *Window
}

// areaH is the height available to windows: everything but the status bar.
func (m *WM) areaH() int { return max(m.rows-1, 1) }

// setBarMessage shows a message in the status bar for d.
func (m *WM) setBarMessage(msg string, d time.Duration) {
	m.barMsg, m.barMsgUntil = msg, time.Now().Add(d)
	m.dirty = true
}

// startBackground starts the full-screen background shell in the launch
// directory.
// snapshot describes the windows for a crash report.
func (m *WM) snapshot() string {
	var b strings.Builder
	fmt.Fprintf(&b, "screen=%dx%d windows=%d", m.cols, m.rows, len(m.windows))
	if m.focused != nil {
		fmt.Fprintf(&b, " focused=%d", m.focused.id)
	}
	if m.sel != nil && m.sel.win != nil {
		fmt.Fprintf(&b, " selection=win%d %+v-%+v", m.sel.win.id, m.sel.anchor, m.sel.head)
	}
	for _, w := range m.windows {
		fmt.Fprintf(&b, "; win%d at %d,%d %dx%d frame=%d bg=%v closed=%v",
			w.id, w.x, w.y, w.w, w.h, w.frame, w.background, w.closed)
		if w.emu != nil {
			cur := w.emu.CursorPosition()
			fmt.Fprintf(&b, " emu=%dx%d cursor=%d,%d alt=%v history=%d",
				w.emu.Width(), w.emu.Height(), cur.X, cur.Y, w.emu.IsAltScreen(), w.emu.ScrollbackLen())
		}
		fmt.Fprintf(&b, " scroll=%d hscroll=%d nowrap=%v program=%q",
			w.scroll, w.hscroll, w.nowrap, w.displayTitle("process"))
	}
	return b.String()
}

func (m *WM) startBackground() error {
	win, err := newWindow(0, m.shell(), 0, 0, m.cols, m.areaH(), winOpts{
		trueColor: m.trueColor, cell: m.cellSize,
		background: true, scrollback: m.cfg.ScrollbackLines, dir: m.dir,
		nowrap: !m.cfg.WrapLines, nowrapW: m.cfg.NowrapWidth,
	}, m.out)
	if err != nil {
		return err
	}
	m.initBackground(win)
	m.restoreDesktop(win)
	m.applyHostColors(win)
	m.hookClipboard(win)
	m.hookEvents(win)
	m.bg = win
	m.windows = slices.Insert(m.windows, 0, win)
	if m.focused == nil {
		m.focus(win)
	}
	m.dirty = true
	return nil
}

func newWM(cfg *Config, warnings []string, cols, rows int) *WM {
	m := &WM{
		cfg: cfg, warnings: warnings, cols: cols, rows: rows,
		// A short queue: when programs write faster than goat can parse,
		// the PTY fills and slows them down (like any terminal), instead of
		// megabytes piling up here (which would make Ctrl+C take effect late).
		nextID: 1, out: make(chan ptyMsg, 16),
		escFire: make(chan struct{}, 1), dirty: true, started: time.Now(),
		bar:       barRight{clock: true}, // main reads local.toml's settings
		local:     defaultLocalOpts(),
		identOut:  make(chan identMsg, 16),
		gitOut:    make(chan gitMsg, 16),
		home:      os.Getenv("HOME"),
		macOption: macOptionOn(cfg.MacOptionKeys, runtime.GOOS, os.Getenv("TERM_PROGRAM"), os.Getenv("LC_TERMINAL")),
	}
	m.optionArrows, m.arrowsLearn = macOptionArrowsOn(cfg.MacOptionArrows, os.Getenv("TERM_PROGRAM"))
	return m
}

// ---- z-order ---------------------------------------------------------------

// raise moves w to the top of its layer: pinned windows above everything,
// unpinned windows just below the first pinned one.
func (m *WM) raise(w *Window) {
	i := slices.Index(m.windows, w)
	if i < 0 || w.background { // the background stays at the bottom
		return
	}
	m.windows = slices.Delete(m.windows, i, i+1)
	pos := len(m.windows)
	if !w.pinned {
		if p := slices.IndexFunc(m.windows, func(x *Window) bool { return x.pinned }); p >= 0 {
			pos = p
		}
	}
	m.windows = slices.Insert(m.windows, pos, w)
}

func (m *WM) focus(w *Window) {
	if w == nil {
		m.focused = nil
		return
	}
	w.minimized = false // focusing a hidden window brings it back (minimize.go)
	if m.focused != w {
		if m.isHelp(m.focused) {
			m.cancelKeyEdit() // a key being changed there isn't waited for any more
		}
		if m.focused != nil && !m.focused.closed {
			m.focused.emu.Blur()
		}
		m.focused = w
		w.emu.Focus()
	}
	m.raise(w)
	m.refreshPlace(w, time.Now())
	m.dirty = true
}

// byID returns the floating windows in creation order (the numbers shown in
// titles and used by Alt+1..9). Stable, unlike the z-order. The events
// window has no number, so showing it doesn't renumber the others.
func (m *WM) byID() []*Window {
	ws := slices.DeleteFunc(slices.Clone(m.windows), func(w *Window) bool { return w.background || m.isPanel(w) })
	slices.SortFunc(ws, func(a, b *Window) int { return a.id - b.id })
	return ws
}

// number is the window's number: 0 for the background, then 1, 2, ...
func (m *WM) number(w *Window) int {
	if w.background {
		return 0
	}
	return slices.Index(m.byID(), w) + 1
}

// cycle is the Alt+n / Alt+p order: the background, then windows by
// number, then the events window if shown.
func (m *WM) cycle() []*Window {
	ws := m.byID()
	if m.bg != nil {
		ws = append([]*Window{m.bg}, ws...)
	}
	if m.eventsShown() {
		ws = append(ws, m.ev.win)
	}
	for _, w := range []*Window{m.help.win, m.statWin.win, m.inv.win} {
		if m.panelShown(w) {
			ws = append(ws, w)
		}
	}
	return ws
}

// tabName is a window's name in its title and status bar tab: "2:vim",
// or "events".
func (m *WM) tabName(w *Window) string {
	if name := m.panelName(w); name != "" { // system.toml's title, if set
		return m.panelTitle(name)
	}
	return fmt.Sprintf("%d:%s", m.number(w), w.displayTitle(m.cfg.TitleSource))
}

// topAt returns the topmost window containing the screen point.
func (m *WM) topAt(x, y int) *Window {
	for i := len(m.windows) - 1; i >= 0; i-- {
		if !m.windows[i].minimized && m.windows[i].contains(x, y) {
			return m.windows[i]
		}
	}
	return nil
}

// ---- PTY events -------------------------------------------------------------

func (m *WM) handlePty(msg ptyMsg) {
	w := msg.w
	if !slices.Contains(m.windows, w) {
		return // already removed
	}
	if msg.exited {
		status := "?"
		if ps := w.cmd.ProcessState; ps != nil {
			status = ps.String()
		}
		logger.Info("window exited", "window", w.id, "background", w.background,
			"program", w.displayTitle("process"), "status", status)
		m.logEvent(w, evExit, status)
		if hold, how := shouldHold(w); hold && !w.background {
			m.holdWindow(w, how)
			return
		}
		hadFocus := m.focused == w
		m.remove(w)
		if w.background {
			m.backgroundExited(hadFocus)
		}
		return
	}
	if err := w.feed(msg.data); err != nil {
		m.setBarMessage(fmt.Sprintf("window %d: %v", m.number(w), err), 10*time.Second)
		m.logEvent(w, evError, err.Error())
	}
	m.dirty = true
}

// initBackground starts a window in the terminal's default colors.
func (m *WM) initBackground(w *Window) {
	w.bgColor, w.fgColor, w.pal = nil, nil, 0
}

// sameColorsAs gives w from's colors (and palette). With no from, w keeps
// its colors.
func sameColorsAs(from, w *Window) {
	if from == nil {
		return
	}
	w.bgColor, w.fgColor, w.pal = from.bgColor, from.fgColor, from.pal
}

// nextColorsFrom gives w the palette after from's: the next background of
// its theme, with the text color in the same place. A from without a
// palette passes on its colors as they are.
func (m *WM) nextColorsFrom(from, w *Window) {
	if from == nil {
		return
	}
	if from.pal == 0 || m.combos == nil {
		sameColorsAs(from, w)
		return
	}
	m.setCombo(w, m.combos.nextCombo(from.pal-1))
}

// backgroundExited: like a terminal, quit when the background shell exits,
// unless floating windows are still open; then start a fresh shell.
func (m *WM) backgroundExited(hadFocus bool) {
	if len(m.windows) == 0 {
		m.quit = true
		return
	}
	focus := m.focused
	if err := m.startBackground(); err != nil {
		m.setBarMessage("background shell: "+err.Error(), 10*time.Second)
		m.logEvent(nil, evError, "background shell: "+err.Error())
		return
	}
	if hadFocus { // it was in use: the new shell takes over
		focus = m.bg
	}
	if focus != nil {
		m.focus(focus)
	}
	m.setBarMessage("background shell exited; started a new one", 4*time.Second)
}

// tick runs twice a second: poll foreground process names, expire status
// messages, and keep the clock current.
// repaint redraws the whole screen with the next frame (see
// hostScreen.repaintAll): keys.repaint, the terminal getting focus back,
// and every repaint_seconds.
func (m *WM) repaint() {
	m.repaintHost = true
	m.dirty = true
}

func (m *WM) tick(now time.Time) {
	if n := m.cfg.RepaintSeconds; n > 0 && now.Sub(m.lastRepaint) >= time.Duration(n)*time.Second {
		m.repaint()
	}
	for _, w := range m.windows {
		if w.refreshProcName() {
			m.dirty = true
		}
		w.dropOrphanKeyboard() // (keyboard.go)
	}
	for _, w := range m.windows { // directories and git (show_git: every window)
		if m.refreshPlace(w, now) {
			m.dirty = true
		}
	}
	if m.refreshBarRight(now) {
		m.dirty = true
	}
	if m.refreshAliases(now) {
		m.dirty = true
	}
	if m.refreshSystem(now) { // system.toml (system.go)
		m.dirty = true
	}
	if m.statusWinShown() || m.inventoryShown() { // status and inventory (Alt+F2, Alt+F3)
		m.procs.request(now)
		m.syncStatusWin(now)
		m.syncInventory(now)
	}
	if m.identOn {
		if m.refreshLocalIdent(now) {
			m.dirty = true
		}
		for _, w := range m.windows {
			if !w.background && m.refreshIdent(w) {
				m.dirty = true
			}
		}
	}
	if m.barMsg != "" && now.After(m.barMsgUntil) {
		m.barMsg = ""
		m.dirty = true
	}
	if mn := now.Hour()*60 + now.Minute(); mn != m.lastMinute {
		m.lastMinute = mn
		m.dirty = true
	}
}

// swapWithBackground swaps the program in the active floating window with
// the background's (Alt+s). Each program keeps everything that is its own:
// its terminal and history, colors, wrapping, scroll position, title,
// directory and identity. What the two exchange are their places: position
// and size, frame, number (the background stays 0) and stacking order. Both
// programs are told their new sizes. The focus stays on the floating
// window, which now runs what was the background.
func (m *WM) swapWithBackground() {
	fw, bg := m.focused, m.bg
	switch {
	case fw == nil || bg == nil || fw.closed || bg.closed:
		return
	case fw.background:
		m.setBarMessage("Alt+s swaps a floating window with the background: focus one first", 3*time.Second)
		return
	case m.isPanel(fw):
		m.setBarMessage("the "+m.tabName(fw)+" window can't be swapped with the background", 3*time.Second)
		return
	}
	fi, bi := slices.Index(m.windows, fw), slices.Index(m.windows, bg)
	if fi < 0 || bi < 0 {
		return
	}
	m.clearSelectionIn(fw)
	m.clearSelectionIn(bg)
	m.drag = drag{}

	x, y, width, height := fw.x, fw.y, fw.w, fw.h
	frame, decorated, pinned := fw.frame, fw.decorated, fw.pinned
	fw.id, bg.id = bg.id, fw.id

	// The floating window's program goes to the background ...
	fw.background, fw.pinned = true, false
	fw.frame, fw.decorated = bg.frame, bg.decorated
	fw.x, fw.y, fw.w, fw.h = 0, 0, m.cols, m.areaH()
	// ... and the background's takes the floating window's place.
	bg.background, bg.pinned = false, pinned
	bg.frame, bg.decorated = frame, decorated
	bg.x, bg.y, bg.w, bg.h = x, y, width, height
	fw.refit()
	bg.refit()

	m.windows[fi], m.windows[bi] = bg, fw
	m.bg = fw
	m.focus(bg) // the same floating window, now running the former background
	for _, w := range []*Window{fw, bg} {
		w.refreshProcName()
		m.refreshPlace(w, time.Now())
		if m.showIdent() {
			m.refreshIdent(w)
		}
	}
	logger.Info("swapped with the background", "window", bg.id,
		"now_floating", bg.displayTitle("process"), "now_background", fw.displayTitle("process"))
	m.setBarMessage(fmt.Sprintf("swapped window %d with the background", m.number(bg)), 2*time.Second)
	m.dirty = true
}

// backgroundTone is the tone of w's background: its palette's theme, its
// color, or for the terminal's own background, the color the terminal
// reported (dark if it didn't).
func (m *WM) backgroundTone(w *Window) tone {
	if c := m.combos.get(w.pal - 1); c != nil {
		return m.combos.tone(c)
	}
	if w.bgColor != nil {
		return toneOf(w.bgColor)
	}
	if m.hostBg != nil {
		return toneOf(m.hostBg)
	}
	return toneDark
}

// cellSize is a character cell in pixels (0, 0 if the terminal didn't say).
func (m *WM) cellSize() (int, int) { return m.cellW, m.cellH }
