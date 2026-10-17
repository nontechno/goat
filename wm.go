package main

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
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
	palette        *palette    // the .color files (nil: no window colors)
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

	status      string      // status bar message
	statusUntil time.Time   // when it disappears
	tabs        []tab       // status bar tab positions, for mouse clicks
	quitAsk     *quitDialog // the "Quit goat?" dialog, while it is up
	lastMinute  int         // clock redraw

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

// setStatus shows a message in the status bar for d.
func (m *WM) setStatus(msg string, d time.Duration) {
	m.status, m.statusUntil = msg, time.Now().Add(d)
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
		escFire: make(chan struct{}, 1), dirty: true,
		bar:       barRight{clock: true}, // main reads local.toml's settings
		identOut:  make(chan identMsg, 16),
		home:      os.Getenv("HOME"),
		palette:   cfg.palette,
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
	ws := slices.DeleteFunc(slices.Clone(m.windows), func(w *Window) bool { return w.background || m.isEvents(w) })
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
	return ws
}

// tabName is a window's name in its title and status bar tab: "2:vim",
// or "events".
func (m *WM) tabName(w *Window) string {
	if m.isEvents(w) {
		return "events"
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

// ---- window lifecycle --------------------------------------------------------

func (m *WM) shell() string {
	if s := strings.TrimSpace(m.cfg.Shell); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv("SHELL")); s != "" {
		return s
	}
	return "/bin/sh"
}

// minH is the smallest usable window height: borders, title rows, one line.
func (m *WM) minH() int {
	_, _, t, b := framePads(false, m.cfg.frame, !m.cfg.DisableMouse)
	return max(m.cfg.Layout.MinWindowRows, t+b+1)
}

func (m *WM) minW() int { return m.cfg.Layout.MinWindowCols }

func (m *WM) newWindow() error {
	_, err := m.openWindow(openSpec{dir: m.newWindowDir(), frame: m.cfg.frame, wrap: m.newWindowWrap()})
	return err
}

// newWindowWrap is whether a new window (Alt+c) wraps long lines: as the
// window it is opened from does (the active one, the background shell
// included); wrap_lines when there is none, or it is the events window.
func (m *WM) newWindowWrap() bool {
	if f := m.focused; f != nil && !f.closed && !m.isEvents(f) {
		return !f.nowrap
	}
	return m.cfg.WrapLines
}

// openSpec describes a window to open: Alt+c's, or a launch script app's.
type openSpec struct {
	dir     string
	command []string // program and arguments (nil: the shell)
	frame   frameStyle
	wrap    bool
	title   string   // fixed title ("": the program's name)
	hold    holdMode // launch scripts: stay open when the program ends
	// Size (whole window; 0: new_window_*_ratio) and, with hasPos, place.
	cols, rows, x, y int
	hasPos           bool
}

// openWindow opens a floating window, cascading from the focused one, with
// the colors new windows get, and focuses it.
func (m *WM) openWindow(o openSpec) (*Window, error) {
	l := m.cfg.Layout
	area := m.areaH()
	decorated := !m.cfg.DisableMouse
	_, _, t, b := framePads(false, o.frame, decorated)
	minH := max(l.MinWindowRows, t+b+1)
	w := clamp(int(float64(m.cols)*l.NewWindowWidthRatio), m.minW(), max(m.cols, m.minW()))
	h := clamp(int(float64(area)*l.NewWindowHeightRatio), minH, max(area, minH))
	if o.cols > 0 {
		w = clamp(o.cols, m.minW(), max(m.cols, m.minW()))
	}
	if o.rows > 0 {
		h = clamp(o.rows, minH, max(area, minH))
	}
	x, y := (m.cols-w)/2, (area-h)/2
	if f := m.focused; f != nil && !f.background {
		x, y = f.x+l.CascadeOffsetX, f.y+l.CascadeOffsetY
	}
	if o.hasPos {
		x, y = min(o.x, max(m.cols-w, 0)), min(o.y, max(area-h, 0))
	}
	if x < 0 || x+w > m.cols {
		x = 0
	}
	if y < 0 || y+h > area {
		y = 0
	}
	opts := winOpts{
		decorated: decorated, frame: o.frame,
		scrollback: m.cfg.ScrollbackLines, dir: o.dir,
		nowrap: !o.wrap, nowrapW: m.cfg.NowrapWidth, command: o.command,
	}
	win, err := newWindow(m.nextID, m.shell(), x, y, w, h, opts, m.out)
	if err != nil && opts.dir != m.dir { // e.g. the directory just vanished
		logger.Warn("new window: can't start in the active window's directory; using the launch folder",
			"dir", opts.dir, "err", err)
		opts.dir = m.dir
		win, err = newWindow(m.nextID, m.shell(), x, y, w, h, opts, m.out)
	}
	if err != nil {
		logger.Error("new window failed", "shell", m.shell(), "command", o.command, "dir", opts.dir, "err", err)
		return nil, err
	}
	logger.Info("window opened", "window", win.id, "shell", m.shell(), "command", o.command, "dir", opts.dir,
		"size", fmt.Sprintf("%dx%d", win.contentW(), win.contentH()))
	m.nextID++
	win.title, win.hold, win.scripted = o.title, o.hold, o.command != nil
	if o.command != nil { // until the program's own name is known
		if f := strings.Fields(o.command[len(o.command)-1]); len(f) > 0 {
			win.shellName = filepath.Base(f[0])
		}
	}
	m.initBackground(win)
	if m.cfg.Theme.NewWindowNextColors {
		m.nextColorsFrom(m.focused, win)
	} else {
		sameColorsAs(m.focused, win)
	}
	m.applyHostColors(win)
	m.hookClipboard(win)
	m.hookEvents(win)
	m.windows = append(m.windows, win)
	m.focus(win)
	return win, nil
}

// newWindowDir is where a new window's shell starts: the active window's
// current directory if it can be used, else the folder goat was started in.
func (m *WM) newWindowDir() string {
	if f := m.focused; f != nil {
		if d := f.currentDir(); usableDir(d) {
			return d
		}
	}
	return m.dir
}

// remove drops a window (closed by the user or exited) and refocuses.
func (m *WM) remove(w *Window) {
	i := slices.Index(m.windows, w)
	if i < 0 {
		return
	}
	w.close()
	m.clearSelectionIn(w)
	m.windows = slices.Delete(m.windows, i, i+1)
	if m.bg == w {
		m.bg = nil
	}
	if m.drag.win == w {
		m.drag = drag{}
	}
	if m.focused == w {
		m.focused = nil
		m.focus(m.topShown())
	}
	m.dirty = true
}

func (m *WM) closeAll() {
	for _, w := range slices.Clone(m.windows) {
		m.remove(w)
	}
}

// ---- geometry ---------------------------------------------------------------

func clamp(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	return max(lo, min(v, hi))
}

// place applies geometry to w, enforcing minimum size and keeping at least
// one column and the top border row on screen so the window stays reachable.
func (m *WM) place(w *Window, x, y, width, height int) {
	if w.background { // always exactly the area above the status bar
		x, y, width, height = 0, 0, m.cols, m.areaH()
		if width != w.w || height != w.h {
			m.clearSelectionIn(w)
		}
		w.setGeometry(x, y, width, height)
		m.dirty = true
		return
	}
	width = max(width, m.minW())
	height = max(height, m.minH())
	x = clamp(x, -(width - 1), m.cols-1)
	y = clamp(y, 0, m.areaH()-1)
	if width != w.w || height != w.h {
		m.clearSelectionIn(w) // content reflows; the selection would be stale
	}
	w.setGeometry(x, y, width, height)
	m.dirty = true
}

func (m *WM) moveFocused(dx, dy int) {
	if w := m.focused; w != nil && !w.background {
		m.place(w, w.x+dx, w.y+dy, w.w, w.h)
	}
}

func (m *WM) resizeFocused(dw, dh int) {
	if w := m.focused; w != nil && !w.background {
		m.place(w, w.x, w.y, w.w+dw, w.h+dh)
	}
}

// resizeScreen handles a terminal resize: keep every window reachable.
func (m *WM) resizeScreen(cols, rows int) {
	m.cols, m.rows = cols, rows
	for _, w := range m.windows {
		m.place(w, w.x, w.y, w.w, w.h)
	}
	m.dirty = true
}

// ---- keyboard --------------------------------------------------------------

func (m *WM) run(a action) error {
	defer m.saveDesktop() // Alt+b / Alt+f / Alt+s may change its colors
	switch a {
	case actNewWindow:
		return m.newWindow()
	case actFocusNext, actFocusPrev:
		ids := m.shownCycle()
		if len(ids) < 2 || m.focused == nil {
			return nil
		}
		i := slices.Index(ids, m.focused)
		if a == actFocusNext {
			i = (i + 1) % len(ids)
		} else {
			i = (i - 1 + len(ids)) % len(ids)
		}
		m.focus(ids[i])
	case actQuit:
		m.askQuit() // asks first when floating windows are open
	case actCloseWindow:
		switch w := m.focused; {
		case w == nil:
		case w.background:
			m.setStatus("the background shell can't be closed: type exit, or Alt+"+m.cfg.Keys.Quit+" to quit", 4*time.Second)
		case m.isEvents(w):
			m.setStatus("Alt+"+m.cfg.Keys.ShowEvents+" hides the events window", 3*time.Second)
		default:
			m.remove(w)
		}
	case actMoveLeft:
		m.moveFocused(-1, 0)
	case actMoveRight:
		m.moveFocused(1, 0)
	case actMoveUp:
		m.moveFocused(0, -1)
	case actMoveDown:
		m.moveFocused(0, 1)
	case actResizeLeft:
		m.resizeFocused(-1, 0)
	case actResizeRight:
		m.resizeFocused(1, 0)
	case actResizeUp:
		m.resizeFocused(0, -1)
	case actResizeDown:
		m.resizeFocused(0, 1)
	case actCycleBackground:
		m.cycleBackground()
	case actCycleForeground:
		m.cycleForeground()
	case actToggleWrap:
		if w := m.focused; w != nil {
			m.clearSelectionIn(w) // its columns may not exist afterwards
			w.setWrap(w.nowrap)
			m.setStatus(fmt.Sprintf("window %d: %s", m.number(w), map[bool]string{
				true:  "long lines run past the edge (scroll sideways: Alt+< / Alt+>, Shift+wheel)",
				false: "long lines wrap"}[w.nowrap]), 3*time.Second)
			m.dirty = true
		}
	case actScrollLeft, actScrollRight:
		if w := m.focused; w != nil {
			step := max(w.contentW()/2, 1)
			if w.scrollSideways(pick(a == actScrollLeft, -step, step)) {
				m.dirty = true
			}
		}
	case actShowEvents:
		m.toggleEvents()
	case actSwapBackground:
		m.swapWithBackground()
	case actRepaint:
		m.repaint()
	case actPinWindow:
		if w := m.focused; w != nil && !w.background {
			w.pinned = !w.pinned
			m.raise(w)
			m.dirty = true
		}
	}
	return nil
}

// shortcut runs the window-manager action for an Alt key, if it is one.
// It reports whether the key was consumed.
func (m *WM) shortcut(k uv.Key) (bool, error) {
	shift := k.Mod.Contains(uv.ModShift)
	switch k.Code {
	case uv.KeyLeft:
		return true, m.run(pick(shift, actResizeLeft, actMoveLeft))
	case uv.KeyRight:
		return true, m.run(pick(shift, actResizeRight, actMoveRight))
	case uv.KeyUp:
		return true, m.run(pick(shift, actResizeUp, actMoveUp))
	case uv.KeyDown:
		return true, m.run(pick(shift, actResizeDown, actMoveDown))
	// History: Alt+PageUp/PageDown by a page, with Shift a few lines;
	// Alt+Home to the oldest line, Alt+End back to the live view.
	case uv.KeyPgUp:
		m.scrollFocused(pick(shift, scrollStep, max(m.focusedPage(), 1)))
		return true, nil
	case uv.KeyPgDown:
		m.scrollFocused(-pick(shift, scrollStep, max(m.focusedPage(), 1)))
		return true, nil
	case uv.KeyHome:
		if w := m.focused; w != nil {
			m.scrollWindow(w, w.emu.ScrollbackLen())
		}
		return true, nil
	case uv.KeyEnd:
		if w := m.focused; w != nil {
			m.scrollWindow(w, -w.scroll)
		}
		return true, nil
	}
	r, ok := bindingRune(k)
	if !ok {
		return false, nil
	}
	if r == '0' {
		switch {
		case m.bg == nil:
		case m.focused == m.bg: // again: hide or bring back the others
			m.toggleHidden()
		default:
			m.focus(m.bg)
		}
		return true, nil
	}
	if r >= '1' && r <= '9' {
		if ids := m.byID(); int(r-'1') < len(ids) {
			m.focus(ids[r-'1'])
		}
		return true, nil
	}
	if a, ok := m.cfg.bindings[r]; ok {
		return true, m.run(a)
	}
	return false, nil
}

// scrollStep is how many lines Alt+Shift+PageUp/PageDown scroll.
const scrollStep = 3

func pick[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}

// learnOptionArrows works out, in macOS Terminal, whether Esc b / Esc f are
// Option+Left / Option+Right (Option types characters: the default) or
// Alt+b / Alt+f ("Use Option as Meta key" is on: then Option+letters arrive
// as Esc + letter too). Any other Alt+letter arriving as Esc + letter means
// Meta is on; an Option character (ç, ∫, ...) means it is off.
func (m *WM) learnOptionArrows(k uv.Key) {
	if r, ok := bindingRune(k); ok && k.Mod.Contains(uv.ModAlt) && r != 'b' && r != 'f' {
		m.optionArrows, m.arrowsLearn = false, false
		logger.Info("macOS Terminal sends Option as Meta: Esc b / Esc f are Alt+b / Alt+f")
		return
	}
	if _, ok := macOptionKey(k); ok {
		m.arrowsLearn = false // Option types characters: Esc b / Esc f are arrows
		logger.Info("macOS Terminal types Option characters: Esc b / Esc f are Option+Left / Right")
	}
}

func (m *WM) handleKey(k uv.Key) error {
	// Keys this terminal sends differently, as goat -learn found out.
	if !m.pasting && !m.escPending {
		k = m.remapKey(k)
	}
	// macOS Terminal: Option+Left / Option+Right arrive as Esc b / Esc f.
	// (Esc then b typed by hand comes as two keys: still Alt+b.)
	if m.arrowsLearn && !m.pasting && !m.escPending {
		m.learnOptionArrows(k)
	}
	if m.optionArrows && !m.pasting && !m.escPending {
		if k2, ok := optionArrowKey(k); ok {
			k = k2
		}
	}
	if m.quitAsk != nil && !m.pasting {
		m.quitDialogKey(k)
		return nil
	}
	if m.pasting { // raw paste without bracketed-paste support upstream
		if k.Text != "" {
			m.pasteBuf = append(m.pasteBuf, k.Text...)
		} else {
			m.pasteBuf = append(m.pasteBuf, encodeKey(k, false)...)
		}
		return nil
	}

	// Esc followed quickly by a character key acts as Alt+key. Anything else
	// is not a shortcut: deliver the held-back Esc, then handle the key.
	if m.escPending {
		m.cancelEsc()
		if _, isChar := bindingRune(k); isChar && (k.Mod == 0 || k.Mod == uv.ModShift) {
			k2 := k
			k2.Mod |= uv.ModAlt
			if ok, err := m.shortcut(k2); ok || err != nil {
				return err
			}
		}
		m.sendFocused([]byte{0x1b})
	}

	if k.Mod.Contains(uv.ModAlt) {
		if ok, err := m.shortcut(k); ok || err != nil {
			return err
		}
	}

	// macOS Option+key typing a character (Option+c = ç): a shortcut if
	// that Alt+key is one, otherwise the character is typed as usual.
	if m.macOption {
		if k2, ok := macOptionKey(k); ok {
			if ok, err := m.shortcut(k2); ok || err != nil {
				return err
			}
		}
	}

	if k.Code == uv.KeyEscape && k.Mod == 0 && m.cfg.AltTimeoutMs > 0 && m.focused != nil {
		m.escPending = true
		m.escTimer = time.AfterFunc(time.Duration(m.cfg.AltTimeoutMs)*time.Millisecond, func() {
			select {
			case m.escFire <- struct{}{}:
			default:
			}
		})
		return nil
	}

	if w := m.focused; m.isEvents(w) {
		m.eventsKey(w, k) // scrolling keys; there is no program to type to
		return nil
	}
	if w := m.focused; w != nil {
		m.clearSelectionIn(w)
		m.sendFocused(encodeKey(k, w.appCursor)) // redraws if it was scrolled back
	}
	return nil
}

func (m *WM) cancelEsc() {
	m.escPending = false
	if m.escTimer != nil {
		m.escTimer.Stop()
	}
	select { // drop a timer that already fired
	case <-m.escFire:
	default:
	}
}

// escExpired: the Esc timeout passed with no key, so it was a plain Esc.
func (m *WM) escExpired() {
	if m.escPending {
		m.escPending = false
		m.sendFocused([]byte{0x1b})
	}
}

func (m *WM) sendFocused(b []byte) {
	if w := m.focused; w != nil && w.held {
		// Its program has ended: Enter closes the window.
		if strings.ContainsAny(string(b), "\r\n") {
			m.remove(w)
		}
		return
	}
	if w := m.focused; w != nil {
		if w.scroll != 0 {
			m.dirty = true
		}
		w.send(b)
	}
}

func (m *WM) paste(text string) {
	if m.quitAsk != nil {
		return
	}
	if m.isEvents(m.focused) {
		m.setStatus("not pasted: the events window takes no input", 3*time.Second)
		return
	}
	if w := m.focused; w != nil && !w.closed && text != "" {
		m.clearSelectionIn(w)
		w.scroll = 0
		w.emu.Paste(text) // bracketed if the program asked for it
		m.dirty = true
	}
}

// ---- scrollback -------------------------------------------------------------

func (m *WM) focusedPage() int {
	if m.focused == nil {
		return 0
	}
	return m.focused.contentH() - 1
}

func (m *WM) scrollFocused(n int) {
	if m.focused != nil {
		m.scrollWindow(m.focused, n)
	}
}

// scrollWindow moves the history view by n lines (positive = older).
func (m *WM) scrollWindow(w *Window, n int) {
	if w.emu.IsAltScreen() {
		return
	}
	s := clamp(w.scroll+n, 0, w.emu.ScrollbackLen())
	if s != w.scroll {
		w.scroll = s
		m.dirty = true
	}
}

// ---- mouse ------------------------------------------------------------------

// forwardMouse passes a mouse event to w's program in content coordinates.
func (m *WM) forwardMouse(w *Window, ev uv.MouseEvent) {
	if w.closed || !w.wantsMouse() {
		return
	}
	mo := ev.Mouse()
	mo.X = clamp(mo.X-w.contentX(), 0, w.contentW()-1)
	mo.Y = clamp(mo.Y-w.contentY(), 0, w.contentH()-1)
	switch ev.(type) {
	case uv.MouseClickEvent:
		w.emu.SendMouse(uv.MouseClickEvent(mo))
	case uv.MouseReleaseEvent:
		w.emu.SendMouse(uv.MouseReleaseEvent(mo))
	case uv.MouseMotionEvent:
		w.emu.SendMouse(uv.MouseMotionEvent(mo))
	case uv.MouseWheelEvent:
		w.emu.SendMouse(uv.MouseWheelEvent(mo))
	}
}

func (m *WM) handleMouse(ev uv.MouseEvent) {
	if m.cfg.DisableMouse {
		return
	}
	if m.quitAsk != nil {
		m.quitDialogMouse(ev)
		return
	}
	mo := ev.Mouse()
	x, y := mo.X, mo.Y

	switch ev.(type) {
	case uv.MouseClickEvent:
		if y == m.rows-1 { // status bar: click a tab to focus that window
			for _, t := range m.tabs {
				if x >= t.x0 && x < t.x1 && slices.Contains(m.windows, t.win) {
					m.focus(t.win)
				}
			}
			return
		}
		w := m.topAt(x, y)
		if w == nil {
			return
		}
		m.focus(w)
		d := drag{win: w}
		shift := mo.Mod.Contains(uv.ModShift)
		switch {
		case mo.Button == uv.MouseLeft && w.onGrip(x, y):
			// Frame "none": the corner resizes, even over a program that
			// uses the mouse.
			d.kind = dragBottomR
		case w.inContent(x, y) && w.wantsMouse() && !shift:
			d.kind = dragContent
			m.forwardMouse(w, ev)
		case w.inContent(x, y):
			// Select text; Shift overrides programs that use the mouse.
			if mo.Button == uv.MouseLeft {
				m.startSelection(w, x, y)
				return
			}
		case mo.Button != uv.MouseLeft:
		case w.onHandle(x, y) < 0: // frame "none": left end of the top line
			d.kind, d.anchorR = dragBottomL, w.x+w.w
		case w.onHandle(x, y) > 0: // ... right end
			d.kind = dragBottomR
		case w.inTitleBar(x, y):
			d.kind, d.offX, d.offY = dragMove, x-w.x, y-w.y
		case w.onBottom(x, y) && w.onLeft(x, y):
			d.kind, d.anchorR = dragBottomL, w.x+w.w
		case w.onBottom(x, y) && w.onRight(x, y):
			d.kind = dragBottomR
		case w.onLeft(x, y):
			d.kind, d.anchorR = dragLeft, w.x+w.w
		case w.onRight(x, y):
			d.kind = dragRight
		case w.onBottom(x, y):
			d.kind = dragBottom
		}
		d.startX, d.startY, d.startW, d.startH = x, y, w.w, w.h
		if d.kind != dragNone {
			m.drag = d
		}

	case uv.MouseMotionEvent:
		d := m.drag
		w := d.win
		if w == nil {
			return
		}
		switch d.kind {
		case dragContent:
			m.forwardMouse(w, ev)
		case dragSelect:
			m.extendSelection(x, y)
		case dragMove:
			m.place(w, x-d.offX, y-d.offY, w.w, w.h)
		case dragRight:
			m.place(w, w.x, w.y, d.startW+x-d.startX, w.h)
		case dragBottom:
			m.place(w, w.x, w.y, w.w, d.startH+y-d.startY)
		case dragBottomR:
			m.place(w, w.x, w.y, d.startW+x-d.startX, d.startH+y-d.startY)
		case dragLeft, dragBottomL:
			nx := min(d.anchorR-d.startW+x-d.startX, d.anchorR-m.minW())
			h := w.h
			if d.kind == dragBottomL {
				h = d.startH + y - d.startY
			}
			m.place(w, nx, w.y, d.anchorR-nx, h)
		}

	case uv.MouseReleaseEvent:
		if m.drag.kind == dragContent && m.drag.win != nil {
			m.forwardMouse(m.drag.win, ev)
		}
		if m.drag.kind == dragSelect {
			m.finishSelection()
		}
		m.drag = drag{}

	case uv.MouseWheelEvent:
		w := m.topAt(x, y)
		if w == nil || y == m.rows-1 {
			return
		}
		sideways := mo.Button == uv.MouseWheelLeft || mo.Button == uv.MouseWheelRight ||
			mo.Mod.Contains(uv.ModShift)
		switch {
		case w.inContent(x, y) && w.wantsMouse() && !mo.Mod.Contains(uv.ModShift):
			m.forwardMouse(w, ev)
		case sideways:
			// Shift+wheel or a sideways wheel: scroll a no-wrap view.
			n := 8
			if mo.Button == uv.MouseWheelUp || mo.Button == uv.MouseWheelLeft {
				n = -n
			}
			if w.scrollSideways(n) {
				m.dirty = true
			}
		case w.emu.IsAltScreen():
			// Like xterm's alternate-scroll mode: wheel = arrow keys, so
			// less, man and friends scroll.
			if k := wheelKey(mo.Button); k != 0 {
				for range 3 {
					w.send(encodeKey(uv.Key{Code: k}, w.appCursor))
				}
			}
		default:
			switch mo.Button {
			case uv.MouseWheelUp:
				m.scrollWindow(w, 3)
			case uv.MouseWheelDown:
				m.scrollWindow(w, -3)
			}
		}
	}
}

func wheelKey(b uv.MouseButton) rune {
	switch b {
	case uv.MouseWheelUp:
		return uv.KeyUp
	case uv.MouseWheelDown:
		return uv.KeyDown
	}
	return 0
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
		m.setStatus(fmt.Sprintf("window %d: %v", m.number(w), err), 10*time.Second)
		m.logEvent(w, evError, err.Error())
	}
	m.dirty = true
}

// initBackground starts a window in the terminal's default colors.
func (m *WM) initBackground(w *Window) {
	w.bgColor, w.fgColor = nil, nil
	w.bgPick, w.fgPick = 0, 0
}

// noPalette is the message for Alt+b / Alt+f without the .color files.
const noPalette = "no window colors: the .color files aren't in the config folder (goat -install fetches them)"

// cycleBackground (Alt+b) gives the active window the next background from
// the .color files.
func (m *WM) cycleBackground() {
	w := m.focused
	if w == nil {
		return
	}
	if p := m.palette; p == nil || len(p.bgs) == 0 {
		m.setStatus(noPalette, 5*time.Second)
		return
	}
	m.nextPaletteBackground(w)
}

// sameColorsAs gives w from's colors (background and text, and where they
// are in the .color lists, so Alt+b / Alt+f go on from there). With no from,
// w keeps its colors.
func sameColorsAs(from, w *Window) {
	if from == nil {
		return
	}
	w.bgColor, w.fgColor = from.bgColor, from.fgColor
	w.bgPick, w.fgPick, w.fgTone = from.bgPick, from.fgPick, from.fgTone
}

// nextColorsFrom gives w the background after from's, with a text color for
// its tone: from's own if it suits, else the first one. Without the .color
// files (or a from), w keeps its colors.
func (m *WM) nextColorsFrom(from, w *Window) {
	p := m.palette
	if p == nil || len(p.bgs) == 0 || from == nil {
		return
	}
	w.bgPick = max(m.bgPosition(from.bgColor), 0)%len(p.bgs) + 1
	bg := p.bgs[w.bgPick-1]
	w.bgColor = bg.c
	w.fgColor, w.fgPick, w.fgTone = nil, 0, bg.tone
	if from.fgPick > 0 && from.fgTone == bg.tone {
		w.fgColor, w.fgPick = from.fgColor, from.fgPick
	} else if fgs := p.fgs[bg.tone]; len(fgs) > 0 {
		w.fgColor, w.fgPick = fgs[0].c, 1
	}
	m.fixText(w)
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
		m.setStatus("background shell: "+err.Error(), 10*time.Second)
		m.logEvent(nil, evError, "background shell: "+err.Error())
		return
	}
	if hadFocus { // it was in use: the new shell takes over
		focus = m.bg
	}
	if focus != nil {
		m.focus(focus)
	}
	m.setStatus("background shell exited; started a new one", 4*time.Second)
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
	}
	for _, w := range m.windows { // directories and git (show_git: every window)
		if m.refreshPlace(w, now) {
			m.dirty = true
		}
	}
	if m.refreshBarRight(now) {
		m.dirty = true
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
	if m.status != "" && now.After(m.statusUntil) {
		m.status = ""
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
		m.setStatus("Alt+s swaps a floating window with the background: focus one first", 3*time.Second)
		return
	case m.isEvents(fw):
		m.setStatus("the events window can't be swapped with the background", 3*time.Second)
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
	m.setStatus(fmt.Sprintf("swapped window %d with the background", m.number(bg)), 2*time.Second)
	m.dirty = true
}

// backgroundTone is the tone of w's background: from the palette, or for
// the terminal's own background, from the color the terminal reported
// (dark if it didn't).
func (m *WM) backgroundTone(w *Window) tone {
	if p := m.palette; p != nil && w.bgPick > 0 && w.bgPick <= len(p.bgs) {
		return p.bgs[w.bgPick-1].tone
	}
	if w.bgColor != nil {
		return toneOf(w.bgColor)
	}
	if m.hostBg != nil {
		return toneOf(m.hostBg)
	}
	return toneDark
}

// colorPosition is where c is in a circle of colors: 0 for the terminal's
// own (nil), i for the i-th of list, -1 if it isn't there. It goes by the
// color itself (whatever the window's remembered pick says), and a color
// that looks the same counts.
func colorPosition(c color.Color, list []namedColor) int {
	if c == nil {
		return 0
	}
	for i, x := range list {
		if looksSame(x.c, c) {
			return i + 1
		}
	}
	return -1
}

// bgPosition is where background c is in the circle Alt+b steps through.
func (m *WM) bgPosition(c color.Color) int {
	if m.palette == nil {
		return colorPosition(c, nil)
	}
	return colorPosition(c, m.palette.bgs)
}

// nextPaletteBackground (Alt+b) gives w the next background: through the
// dark ones, then the light ones, then back to the terminal's own. Its text
// color stays as it is (Alt+f changes that).
func (m *WM) nextPaletteBackground(w *Window) {
	p := m.palette
	// The backgrounds form a circle: the terminal's own, then each of the
	// list. Go on from wherever w's current background is in it (one not
	// in the list: from the start), to the next one that looks different
	// (e.g. a black in the list is passed over when the terminal's own
	// background is black too).
	n := len(p.bgs) + 1
	// What is on screen now, and would be (nil: the terminal's own, if it
	// hasn't said which color that is).
	seen := func(c color.Color) color.Color {
		if c == nil {
			return m.hostBg
		}
		return c
	}
	shown := seen(w.bgColor)
	pos := max(m.bgPosition(w.bgColor), 0)
	pick := -1
	for i := 1; i < n; i++ {
		cand := (pos + i) % n
		var c color.Color
		if cand > 0 {
			c = p.bgs[cand-1].c
		}
		if a, b := seen(c), shown; (a == nil) != (b == nil) || !looksSame(a, b) {
			pick = cand
			break
		}
	}
	if pick < 0 {
		m.setStatus("no other background color", 3*time.Second)
		return
	}
	w.bgPick = pick
	msg := "background: the terminal's own"
	w.bgColor = nil
	if w.bgPick > 0 {
		bg := p.bgs[w.bgPick-1]
		w.bgColor = bg.c
		msg = fmt.Sprintf("background %d/%d: %s (%s)", w.bgPick, len(p.bgs), bg.name, bg.tone)
	}
	if m.fixText(w) {
		msg += " - text color changed to " + m.textName(w) + " (the old one wouldn't show)"
	} else if t := m.backgroundTone(w); w.fgPick > 0 && w.fgTone != t {
		msg += fmt.Sprintf(" - text color is one for %s backgrounds: Alt+f", w.fgTone)
	}
	m.applyHostColors(w) // programs asking for the colors get these
	m.setStatus(msg, 3*time.Second)
	m.dirty = true
}

// cycleForeground (Alt+f) gives w the next text color among those for the
// tone of its background (dark or light), then back to the terminal's own.
func (m *WM) cycleForeground() {
	w := m.focused
	if w == nil {
		return
	}
	p := m.palette
	if p == nil {
		m.setStatus(noPalette, 5*time.Second)
		return
	}
	t := m.backgroundTone(w)
	list := p.fgs[t]
	if len(list) == 0 {
		m.setStatus(fmt.Sprintf("no text colors for %s backgrounds (%s-foreground.color)", t, t), 4*time.Second)
		return
	}
	// The text colors form a circle too: the terminal's own, then the list.
	// Go on from w's current text color (one not in the list: from the
	// start) to the next that both shows on the background and looks
	// different from the current one.
	colorOf := func(i int) color.Color {
		if i == 0 {
			return nil
		}
		return list[i-1].c
	}
	n := len(list) + 1
	seen := func(c color.Color) color.Color {
		if c == nil {
			return m.hostFg
		}
		return c
	}
	shown := seen(w.fgColor)
	pos := max(colorPosition(w.fgColor, list), 0)
	pick, skipped := -1, 0
	for i := 1; i < n; i++ {
		cand := (pos + i) % n
		c := colorOf(cand)
		if a, b := seen(c), shown; (a == nil) == (b == nil) && looksSame(a, b) {
			continue
		}
		if !m.readable(c, w.bgColor) {
			skipped++
			continue
		}
		pick = cand
		break
	}
	if pick < 0 {
		m.setStatus("no other text color shows on this background", 3*time.Second)
		return
	}
	w.fgPick, w.fgTone, w.fgColor = pick, t, colorOf(pick)
	msg := "text color: the terminal's own"
	if pick > 0 {
		msg = fmt.Sprintf("text color %d/%d: %s (for %s backgrounds)", pick, len(list), list[pick-1].name, t)
	}
	if skipped > 0 {
		msg += fmt.Sprintf(" - skipped %d that wouldn't show", skipped)
	}
	m.applyHostColors(w)
	m.setStatus(msg, 3*time.Second)
	m.dirty = true
}
