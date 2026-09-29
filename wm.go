package main

import (
	"fmt"
	"image/color"
	"os"
	"runtime"
	"slices"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
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
	cfg        *Config
	warnings   []string
	cols, rows int
	macOption  bool   // treat macOS Option characters as Alt+key (mac_option_keys)
	home       string // $HOME, shown as ~ in the status bar

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

	sel          *selection             // current text selection, if any
	clicks       int                    // 1, 2, 3 for single/double/triple click
	lastClick    time.Time              // for multi-click detection
	lastClickPos selPos                 //
	lastClickWin *Window                //
	copy         func(text, who string) // puts text on the clipboard; who: "" or a program

	status      string    // status bar message
	statusUntil time.Time // when it disappears
	tabs        []tab     // status bar tab positions, for mouse clicks
	lastMinute  int       // clock redraw

	dirty bool
	quit  bool
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
func (m *WM) startBackground() error {
	win, err := newWindow(0, m.shell(), 0, 0, m.cols, m.areaH(), winOpts{
		background: true, scrollback: m.cfg.ScrollbackLines, dir: m.dir,
		nowrap: !m.cfg.WrapLines, nowrapW: m.cfg.NowrapWidth,
	}, m.out)
	if err != nil {
		return err
	}
	m.initBackground(win)
	m.applyHostColors(win)
	m.hookClipboard(win)
	m.bg = win
	m.windows = slices.Insert(m.windows, 0, win)
	if m.focused == nil {
		m.focus(win)
	}
	m.dirty = true
	return nil
}

func newWM(cfg *Config, warnings []string, cols, rows int) *WM {
	return &WM{
		cfg: cfg, warnings: warnings, cols: cols, rows: rows,
		// A short queue: when programs write faster than goat can parse,
		// the PTY fills and slows them down (like any terminal), instead of
		// megabytes piling up here (which would make Ctrl+C take effect late).
		nextID: 1, out: make(chan ptyMsg, 16),
		escFire: make(chan struct{}, 1), dirty: true,
		home:      os.Getenv("HOME"),
		macOption: macOptionOn(cfg.MacOptionKeys, runtime.GOOS, os.Getenv("TERM_PROGRAM"), os.Getenv("LC_TERMINAL")),
	}
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
	if m.focused != w {
		if m.focused != nil && !m.focused.closed {
			m.focused.emu.Blur()
		}
		m.focused = w
		w.emu.Focus()
	}
	m.raise(w)
	if m.cfg.StatusShowDir {
		w.refreshDir()
	}
	m.dirty = true
}

// byID returns the floating windows in creation order (the numbers shown in
// titles and used by Alt+1..9). Stable, unlike the z-order.
func (m *WM) byID() []*Window {
	ws := slices.DeleteFunc(slices.Clone(m.windows), func(w *Window) bool { return w.background })
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

// cycle is the Alt+n / Alt+p order: the background, then windows by number.
func (m *WM) cycle() []*Window {
	ws := m.byID()
	if m.bg != nil {
		ws = append([]*Window{m.bg}, ws...)
	}
	return ws
}

// topAt returns the topmost window containing the screen point.
func (m *WM) topAt(x, y int) *Window {
	for i := len(m.windows) - 1; i >= 0; i-- {
		if m.windows[i].contains(x, y) {
			return m.windows[i]
		}
	}
	return nil
}

// ---- window lifecycle --------------------------------------------------------

func (m *WM) shell() string {
	if m.cfg.Shell != "" {
		return m.cfg.Shell
	}
	if s := os.Getenv("SHELL"); s != "" {
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
	l := m.cfg.Layout
	area := m.areaH()
	w := clamp(int(float64(m.cols)*l.NewWindowWidthRatio), m.minW(), max(m.cols, m.minW()))
	h := clamp(int(float64(area)*l.NewWindowHeightRatio), m.minH(), max(area, m.minH()))
	x, y := (m.cols-w)/2, (area-h)/2
	if f := m.focused; f != nil && !f.background {
		x, y = f.x+l.CascadeOffsetX, f.y+l.CascadeOffsetY
	}
	if x < 0 || x+w > m.cols {
		x = 0
	}
	if y < 0 || y+h > area {
		y = 0
	}
	opts := winOpts{
		decorated: !m.cfg.DisableMouse, frame: m.cfg.frame,
		scrollback: m.cfg.ScrollbackLines, dir: m.newWindowDir(),
		nowrap: !m.cfg.WrapLines, nowrapW: m.cfg.NowrapWidth,
	}
	win, err := newWindow(m.nextID, m.shell(), x, y, w, h, opts, m.out)
	if err != nil && opts.dir != m.dir { // e.g. the directory just vanished
		opts.dir = m.dir
		win, err = newWindow(m.nextID, m.shell(), x, y, w, h, opts, m.out)
	}
	if err != nil {
		return err
	}
	m.nextID++
	m.initBackground(win)
	if m.cfg.Theme.NewWindowNextColors {
		m.nextColorsFrom(m.focused, win)
	}
	m.applyHostColors(win)
	m.hookClipboard(win)
	m.windows = append(m.windows, win)
	m.focus(win)
	return nil
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
		if n := len(m.windows); n > 0 {
			m.focus(m.windows[n-1])
		}
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
	switch a {
	case actNewWindow:
		return m.newWindow()
	case actFocusNext, actFocusPrev:
		ids := m.cycle()
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
		m.quit = true
	case actCloseWindow:
		switch w := m.focused; {
		case w == nil:
		case w.background:
			m.setStatus("the background shell can't be closed: type exit, or Alt+"+m.cfg.Keys.Quit+" to quit", 4*time.Second)
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
	case actToggleWrap:
		if w := m.focused; w != nil {
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
		if m.bg != nil {
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

func (m *WM) handleKey(k uv.Key) error {
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

	if w := m.focused; w != nil {
		m.clearSelectionIn(w)
		w.send(encodeKey(k, w.appCursor))
		m.dirty = m.dirty || w.scroll != 0
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
	if w := m.focused; w != nil {
		if w.scroll != 0 {
			m.dirty = true
		}
		w.send(b)
	}
}

func (m *WM) paste(text string) {
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
		hadFocus := m.focused == w
		m.remove(w)
		if w.background {
			m.backgroundExited(hadFocus)
		}
		return
	}
	if err := w.feed(msg.data); err != nil {
		m.setStatus(fmt.Sprintf("window %d: %v", m.number(w), err), 10*time.Second)
	}
	m.dirty = true
}

// initBackground starts a window in the terminal's default colors, at the
// "default:default" entry of theme.window_colors if there is one.
func (m *WM) initBackground(w *Window) {
	w.bgColor, w.fgColor, w.colorIndex = nil, nil, -1
	for i, c := range m.cfg.Theme.WindowColors {
		if c.Bg == nil && c.Fg == nil {
			w.colorIndex = i
			break
		}
	}
}

// cycleBackground gives the active window the next color scheme from
// theme.window_colors (background and text), wrapping around.
func (m *WM) cycleBackground() {
	w := m.focused
	if w == nil {
		return
	}
	list := m.cfg.Theme.WindowColors
	if len(list) == 0 {
		m.setStatus("no window_colors in the config", 3*time.Second)
		return
	}
	w.colorIndex = (w.colorIndex + 1) % len(list)
	w.bgColor, w.fgColor = list[w.colorIndex].Bg, list[w.colorIndex].Fg
	m.applyHostColors(w) // programs asking for the colors get these
	m.setStatus(fmt.Sprintf("colors %d/%d: %s:%s", w.colorIndex+1, len(list),
		colorName(w.bgColor), colorName(w.fgColor)), 2*time.Second)
}

// nextColorsFrom gives w the window_colors scheme after from's, skipping any
// that look the same as from's (e.g. a list starting "default:default" when
// from uses the terminal's colors). With no from, w keeps its colors.
func (m *WM) nextColorsFrom(from, w *Window) {
	list := m.cfg.Theme.WindowColors
	if from == nil || len(list) == 0 {
		return
	}
	i := from.colorIndex // -1 when from's colors aren't in the list
	for range list {
		i = (i + 1) % len(list)
		if !sameColor(list[i].Bg, from.bgColor) || !sameColor(list[i].Fg, from.fgColor) {
			break
		}
	}
	w.colorIndex, w.bgColor, w.fgColor = i, list[i].Bg, list[i].Fg
}

func sameColor(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

func colorName(c color.Color) string {
	switch c := c.(type) {
	case nil:
		return "default"
	case ansi.IndexedColor:
		return fmt.Sprint(uint8(c))
	default:
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
	}
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
func (m *WM) tick(now time.Time) {
	for _, w := range m.windows {
		if w.refreshProcName() {
			m.dirty = true
		}
	}
	if f := m.focused; f != nil && m.cfg.StatusShowDir && f.refreshDir() {
		m.dirty = true
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
