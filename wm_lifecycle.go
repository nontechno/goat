package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Window lifecycle and geometry: opening, closing, placing windows.

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
	if f := m.focused; f != nil && !f.closed && !m.isPanel(f) {
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
	w, h := m.newWindowSize(minH, area) // local.toml new_window_size, or the ratios
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
		trueColor: m.trueColor, cell: m.cellSize,
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
	hadFloating := len(m.floatingWindows()) > 0
	defer func() {
		// local.toml exit_on_last_window: the last floating window is gone.
		if m.local.exitOnLastWindow && hadFloating && len(m.floatingWindows()) == 0 && !m.quit {
			logger.Info("last window closed: quitting (local.toml exit_on_last_window)")
			m.quit = true
		}
	}()
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
