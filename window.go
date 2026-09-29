package main

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// Window is one floating terminal window.
//
// Everything except the goroutines started in spawn is owned by the main
// event loop goroutine; the goroutines only talk to it through channels and
// the input queue.
type Window struct {
	id         int
	x, y, w, h int // outer geometry, including borders
	frame      frameStyle
	decorated  bool // title shown (mouse enabled)
	pinned     bool
	background bool // the borderless full-screen shell under everything

	emu  *vt.Emulator
	ptmx *os.File
	cmd  *exec.Cmd
	in   *inputQueue

	shellName    string
	procName     string // foreground process, from /proc
	nowrap       bool   // long lines are not wrapped (see emuWidth)
	nowrapW      int
	hscroll      int    // columns the view is scrolled right (no-wrap)
	followCursor bool   // the view follows the cursor sideways (after typing)
	oscTitle     string // set by the program (OSC 0/2)
	oscDir       string // working directory the shell reported (OSC 7)
	dir          string // last known working directory (for the status bar)

	appCursor     bool
	mouseModes    map[ansi.DECMode]bool
	cursorVisible bool
	cursorShape   uv.CursorShape
	cursorBlink   bool

	scroll int // lines scrolled back into history (0 = live)

	exited atomic.Bool // set by the goroutine that waits for the process

	onClipboard func(osc []byte) // OSC 52 from the program ("52;c;<base64>")

	bgColor    color.Color // colors chosen with Alt+b (nil = terminal default)
	fgColor    color.Color //
	colorIndex int         // position in theme.window_colors (-1 = none)
	closed     bool
}

// ptyMsg carries output from a window's PTY, or its exit, to the event loop.
type ptyMsg struct {
	w      *Window
	data   []byte
	exited bool
}

// frameStyle is how a floating window is framed (config: frame).
type frameStyle int

const (
	frameFull    frameStyle = iota // box, with a three-row title box
	frameCompact                   // box, title in the top border
	frameNone                      // only the top line, with the title
)

func parseFrame(s string) (frameStyle, bool) {
	switch s {
	case "full":
		return frameFull, true
	case "compact":
		return frameCompact, true
	case "none":
		return frameNone, true
	}
	return frameFull, false
}

// framePads returns the rows/columns of chrome on each side of the content.
func framePads(background bool, f frameStyle, decorated bool) (left, right, top, bottom int) {
	switch {
	case background:
		return 0, 0, 0, 0
	case f == frameNone:
		return 0, 0, 1, 0
	case f == frameFull && decorated:
		return 1, 1, 3, 1 // top border, title row, separator
	default:
		return 1, 1, 1, 1
	}
}

func (w *Window) pads() (left, right, top, bottom int) {
	return framePads(w.background, w.frame, w.decorated)
}

func (w *Window) chromeH() int {
	_, _, t, b := w.pads()
	return t + b
}
func (w *Window) contentX() int { l, _, _, _ := w.pads(); return w.x + l }
func (w *Window) contentY() int { _, _, t, _ := w.pads(); return w.y + t }
func (w *Window) contentW() int {
	l, r, _, _ := w.pads()
	return max(w.w-l-r, 1)
}
func (w *Window) contentH() int { return max(w.h-w.chromeH(), 1) }

// winOpts are the settings a new window is created with.
type winOpts struct {
	decorated  bool // title bar (mouse enabled)
	frame      frameStyle
	background bool   // borderless full-screen background shell
	scrollback int    // history lines
	dir        string // working directory ("" = inherit)
	nowrap     bool   // long lines run past the right edge (wrap_lines = false)
	nowrapW    int    // emulator width when not wrapping (nowrap_width)
}

// newWindow starts shell in a PTY sized to the window's content area.
func newWindow(id int, shell string, x, y, w, h int, o winOpts, out chan<- ptyMsg) (*Window, error) {
	win := &Window{
		id: id, x: x, y: y, w: w, h: h,
		decorated: o.decorated, frame: o.frame, background: o.background,
		mouseModes:    map[ansi.DECMode]bool{},
		cursorVisible: true,
		cursorBlink:   true, // CSI 0 q: the terminal's default blinking block
		nowrap:        o.nowrap, nowrapW: o.nowrapW,
	}
	cw, ch := win.contentW(), win.contentH()

	args := strings.Fields(shell)
	if len(args) == 0 {
		return nil, errors.New("no shell to start")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = childEnv()
	cmd.Dir = o.dir
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cw), Rows: uint16(ch)})
	if err != nil {
		return nil, err
	}
	ptmx = pollable(ptmx)
	win.cmd, win.ptmx = cmd, ptmx
	win.shellName = filepath.Base(args[0])

	win.emu = vt.NewEmulator(win.emuWidth(cw), ch)
	win.emu.SetLogger(vtLogger{win: id}) // log_level = "debug": unhandled sequences
	win.emu.SetScrollbackSize(o.scrollback)
	// OSC 52: a program in the window sets the clipboard (vim, tmux, ...).
	win.emu.RegisterOscHandler(52, func(data []byte) bool {
		if win.onClipboard != nil {
			win.onClipboard(data)
		}
		return true
	})
	win.emu.SetCallbacks(vt.Callbacks{
		Title:            func(s string) { win.oscTitle = s },
		WorkingDirectory: func(s string) { win.oscDir = parseOSC7(s) },
		CursorVisibility: func(v bool) { win.cursorVisible = v },
		CursorStyle: func(s vt.CursorStyle, steady bool) {
			// x/vt passes "steady", not "blink": CSI 5 q (blinking bar)
			// arrives as (bar, false), CSI 2 q (steady block) as (block, true).
			win.cursorShape, win.cursorBlink = uv.CursorShape(s), !steady
		},
		EnableMode:  func(m ansi.Mode) { win.setMode(m, true) },
		DisableMode: func(m ansi.Mode) { win.setMode(m, false) },
	})

	// Input path: keys we encode and the emulator's own replies (cursor
	// position reports, device attributes, mouse, paste) are queued without
	// blocking and written to the PTY by a dedicated goroutine, so a child
	// that is busy writing output can never deadlock the event loop.
	win.in = newInputQueue()
	go func() { _, _ = io.Copy(win.in, win.emu) }()
	go win.in.drainTo(ptmx)

	go func() { // PTY output -> event loop
		buf := make([]byte, 16*1024) // small chunks keep parsing interruptible
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				out <- ptyMsg{w: win, data: append([]byte(nil), buf[:n]...)}
			}
			if err != nil {
				// EIO: the program side closed (it exited); ErrClosed: the
				// window was closed. Anything else is unexpected.
				if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) && !errors.Is(err, syscall.EIO) {
					logger.Warn("reading program output failed", "window", id, "err", err)
				}
				return
			}
		}
	}()
	go func() { // process exit -> event loop (also reaps the child)
		_ = cmd.Wait()
		win.exited.Store(true)
		out <- ptyMsg{w: win, exited: true}
	}()
	return win, nil
}

// childEnv is the environment for shells: the vt emulator is xterm-like.
func childEnv() []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TERM=") || strings.HasPrefix(kv, "COLORTERM=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
}

func (w *Window) setMode(m ansi.Mode, on bool) {
	d, ok := m.(ansi.DECMode)
	if !ok {
		return
	}
	switch d {
	case ansi.ModeCursorKeys:
		w.appCursor = on
	case ansi.ModeMouseX10, ansi.ModeMouseNormal, ansi.ModeMouseHighlight,
		ansi.ModeMouseButtonEvent, ansi.ModeMouseAnyEvent:
		w.mouseModes[d] = on
	}
}

// wantsMouse reports whether the program enabled mouse reporting.
func (w *Window) wantsMouse() bool {
	for _, on := range w.mouseModes {
		if on {
			return true
		}
	}
	return false
}

// feed passes PTY output to the emulator.
//
// Should the emulator panic on some input, only this window suffers: the
// terminal is reset and an error returned, rather than every window and the
// user's session going down with it.
func (w *Window) feed(data []byte) (err error) {
	if w.closed {
		return nil
	}
	defer func() {
		if p := recover(); p != nil {
			cur := w.emu.CursorPosition()
			logger.Error("emulator failed; window reset",
				"window", w.id, "panic", fmt.Sprint(p),
				"emu", fmt.Sprintf("%dx%d", w.emu.Width(), w.emu.Height()),
				"cursor", fmt.Sprintf("%d,%d", cur.X, cur.Y), "alt_screen", w.emu.IsAltScreen(),
				"nowrap", w.nowrap, "program", w.displayTitle("process"),
				// the output it failed on: enough to reproduce with vt alone
				"output", excerpt(data, 2048),
				"stack", string(debug.Stack()))
			err = fmt.Errorf("terminal emulator failed (%v); window reset", p)
			func() {
				defer func() { _ = recover() }()
				_, _ = w.emu.Write([]byte(ansi.ResetInitialState))
			}()
			w.scroll = 0
		}
	}()
	before := w.emu.ScrollbackPushed()
	_, _ = w.emu.Write(data)
	if w.scroll > 0 { // keep the viewed history lines steady
		added := w.emu.ScrollbackPushed() - before // counted even when the history is full
		w.scroll = min(w.scroll+max(added, 0), w.emu.ScrollbackLen())
	}
	return nil
}

// send writes raw input to the program and returns to the live view.
func (w *Window) send(b []byte) {
	if w.closed || len(b) == 0 {
		return
	}
	w.scroll = 0
	w.followCursor = true // typing: keep the cursor in view (no-wrap)
	_, _ = w.in.Write(b)
}

// setGeometry moves/resizes the window and resizes the PTY and emulator when
// the content area changed.
func (w *Window) setGeometry(x, y, width, height int) {
	oldW, oldH := w.contentW(), w.contentH()
	w.x, w.y, w.w, w.h = x, y, width, height
	cw, ch := w.contentW(), w.contentH()
	if cw != oldW || ch != oldH {
		w.emu.Resize(w.emuWidth(cw), ch)
		setWinsize(w.ptmx, cw, ch)
		w.scroll = min(w.scroll, w.emu.ScrollbackLen())
		w.hscroll = min(w.hscroll, w.maxHScroll())
	}
}

// Line wrapping.
//
// Wrapped (the default), the emulator is exactly as wide as the window and
// long lines wrap at its edge, as in any terminal. Not wrapped, the emulator
// is nowrapW columns wide while the program is still told the window's
// width (the PTY size): full-screen programs lay themselves out for what is
// visible, and only plain output (cat, logs) runs on past the edge. The view
// then scrolls sideways (hscroll).

// emuWidth is the emulator width for a content area cw wide.
func (w *Window) emuWidth(cw int) int {
	if w.nowrap {
		return max(cw, w.nowrapW)
	}
	return cw
}

// maxHScroll is how far the view can scroll right.
func (w *Window) maxHScroll() int { return max(w.emu.Width()-w.contentW(), 0) }

// setWrap switches wrapping on or off. Turning it on cuts lines at the
// window's width, like a terminal made narrower.
func (w *Window) setWrap(wrap bool) {
	if w.nowrap == !wrap {
		return
	}
	w.nowrap = !wrap
	w.emu.Resize(w.emuWidth(w.contentW()), w.contentH())
	w.hscroll = 0
}

// scrollSideways moves the view n columns right (negative: left); reports a
// change. A manual scroll stops the view from following the cursor.
func (w *Window) scrollSideways(n int) bool {
	s := clamp(w.hscroll+n, 0, w.maxHScroll())
	w.followCursor = false
	if s == w.hscroll {
		return false
	}
	w.hscroll = s
	return true
}

// followCursorView scrolls the view sideways, after typing, so that the
// cursor is visible; reports a change.
func (w *Window) followCursorView() bool {
	if !w.followCursor || w.scroll > 0 || w.maxHScroll() == 0 {
		return false
	}
	cw, x := w.contentW(), w.emu.CursorPosition().X
	s := w.hscroll
	switch {
	case x < s:
		s = max(x-cw/4, 0)
	case x >= s+cw:
		s = min(x-cw+1+cw/4, w.maxHScroll())
	}
	if s == w.hscroll {
		return false
	}
	w.hscroll = s
	return true
}

// close ends the window: hang up the shell and release resources. The exit
// goroutine reaps the process; a child that ignores SIGHUP is killed.
func (w *Window) close() {
	if w.closed {
		return
	}
	w.closed = true
	// Stop the goroutine copying the emulator's replies by closing the
	// reply pipe. Not emu.Close(): it sets a flag that Emulator.Read checks
	// without synchronization (a data race in x/vt).
	if pw, ok := w.emu.InputPipe().(*io.PipeWriter); ok {
		_ = pw.CloseWithError(io.EOF)
	}
	w.in.Close()
	_ = w.ptmx.Close()
	if p := w.cmd.Process; p != nil && !w.exited.Load() {
		_ = syscall.Kill(-p.Pid, syscall.SIGHUP) // whole process group
		_ = p.Signal(syscall.SIGHUP)
		time.AfterFunc(3*time.Second, func() { _ = p.Kill() })
	}
}

// refreshProcName updates the foreground process name; reports a change.
func (w *Window) refreshProcName() bool {
	if w.closed {
		return false
	}
	name := foregroundProcessName(w.ptmx, w.cmd)
	if name == "" || name == w.procName {
		return false
	}
	w.procName = name
	return true
}

// currentDir is the working directory of the program in the window: the
// foreground process's (Linux), else what the shell last reported (OSC 7).
func (w *Window) currentDir() string {
	if w.closed || w.ptmx == nil || w.cmd == nil {
		return w.oscDir
	}
	if d := foregroundDir(w.ptmx, w.cmd); d != "" {
		return d
	}
	return w.oscDir
}

// refreshDir updates w.dir; reports a change.
func (w *Window) refreshDir() bool {
	d := w.currentDir()
	if d == "" || d == w.dir {
		return false
	}
	w.dir = d
	return true
}

// parseOSC7 turns an OSC 7 report ("file://host/path", percent-encoded) into
// a path; anything else is taken as a plain path.
func parseOSC7(s string) string {
	if u, err := url.Parse(s); err == nil && u.Scheme == "file" {
		return u.Path
	}
	if strings.HasPrefix(s, "/") {
		return s
	}
	return ""
}

// usableDir reports whether a new shell can start in d.
func usableDir(d string) bool {
	if d == "" {
		return false
	}
	fi, err := os.Stat(d)
	return err == nil && fi.IsDir() && syscall.Access(d, 1 /* X_OK */) == nil
}

// displayTitle is the name shown in the title bar.
func (w *Window) displayTitle(source string) string {
	if source == "terminal" && w.oscTitle != "" {
		return w.oscTitle
	}
	if w.procName != "" {
		return w.procName
	}
	return w.shellName
}

// cellAt returns the cell to show at content position (x, y), taking the
// scrollback offset into account. It never returns nil.
func (w *Window) cellAt(x, y int) *uv.Cell {
	var c *uv.Cell
	if w.scroll > 0 {
		sb := w.emu.ScrollbackLen()
		line := sb - w.scroll + y // index into history ++ screen
		if line < sb {
			c = w.emu.ScrollbackCellAt(x, line)
		} else {
			c = w.emu.CellAt(x, line-sb)
		}
	} else {
		c = w.emu.CellAt(x, y)
	}
	if c == nil {
		return &uv.EmptyCell
	}
	return c
}

// Hit testing, in screen coordinates.

func (w *Window) contains(x, y int) bool {
	return x >= w.x && x < w.x+w.w && y >= w.y && y < w.y+w.h
}

func (w *Window) inContent(x, y int) bool {
	return x >= w.contentX() && x < w.contentX()+w.contentW() &&
		y >= w.contentY() && y < w.contentY()+w.contentH()
}

// inTitleBar: the top border row, plus the title row of a full frame.
func (w *Window) inTitleBar(x, y int) bool {
	if w.background {
		return false
	}
	last := w.y
	if w.decorated && w.frame == frameFull {
		last = w.y + 1
	}
	return x >= w.x && x < w.x+w.w && y >= w.y && y <= last
}

// Edges. The background window has none; frame "none" has no side or
// bottom edges (the ends of its top line are resize handles instead).
func (w *Window) hasEdges() bool { return !w.background && w.frame != frameNone }

func (w *Window) onLeft(x, y int) bool {
	return w.hasEdges() && x == w.x && w.contains(x, y)
}
func (w *Window) onRight(x, y int) bool {
	return w.hasEdges() && x == w.x+w.w-1 && w.contains(x, y)
}
func (w *Window) onBottom(x, y int) bool {
	return w.hasEdges() && y == w.y+w.h-1 && w.contains(x, y)
}

// onHandle reports which end of a frame-"none" top line is at (x, y):
// -1 left, +1 right, 0 neither.
func (w *Window) onHandle(x, y int) int {
	if w.background || w.frame != frameNone || y != w.y {
		return 0
	}
	switch x {
	case w.x:
		return -1
	case w.x + w.w - 1:
		return 1
	}
	return 0
}

// onGrip reports whether (x, y) is in the bottom-right 2x2 cells of a
// frame-"none" window: a resize grip over the content's corner, since such
// a window has no bottom or right edge to drag.
func (w *Window) onGrip(x, y int) bool {
	return !w.background && w.frame == frameNone && w.contains(x, y) &&
		x >= w.x+w.w-2 && y >= w.y+w.h-2 && y > w.y
}

// inputQueue is an unbounded, non-blocking byte queue drained into the PTY.
type inputQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newInputQueue() *inputQueue {
	q := &inputQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *inputQueue) Write(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return 0, io.ErrClosedPipe
	}
	q.buf = append(q.buf, p...)
	q.cond.Signal()
	return len(p), nil
}

func (q *inputQueue) Close() {
	q.mu.Lock()
	q.closed = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *inputQueue) drainTo(dst io.Writer) {
	for {
		q.mu.Lock()
		for len(q.buf) == 0 && !q.closed {
			q.cond.Wait()
		}
		if q.closed {
			q.mu.Unlock()
			return
		}
		data := q.buf
		q.buf = nil
		q.mu.Unlock()
		if _, err := dst.Write(data); err != nil {
			if !errors.Is(err, os.ErrClosed) && !errors.Is(err, syscall.EIO) {
				logger.Warn("writing to program failed; input dropped from now on",
					"err", err, "bytes", len(data))
			}
			return
		}
	}
}

// pollable returns the PTY master as a non-blocking file managed by Go's
// poller. creack/pty hands it over in blocking mode (its ioctls call Fd(),
// which switches a file to blocking), and then Close can't interrupt the
// reader's pending Read: a closed window's reader goroutine and PTY would
// live on for as long as anything (a job that ignores SIGHUP) keeps the
// terminal open. Afterwards, Fd() must not be called on it again; ioctls go
// through ptyControl.
func pollable(f *os.File) *os.File {
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return f
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return f
	}
	nf := os.NewFile(uintptr(fd), f.Name())
	_ = f.Close()
	return nf
}

// ptyControl runs fn with the PTY's descriptor without making it blocking.
func ptyControl(f *os.File, fn func(fd int)) {
	if f == nil {
		return
	}
	if sc, err := f.SyscallConn(); err == nil {
		_ = sc.Control(func(fd uintptr) { fn(int(fd)) })
	}
}

// setWinsize tells the program in the PTY its window size (TIOCSWINSZ).
func setWinsize(f *os.File, cols, rows int) {
	ptyControl(f, func(fd int) {
		_ = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
	})
}
