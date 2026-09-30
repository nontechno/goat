//go:build unix

package pasture

import (
	"errors"
	"fmt"
	"io"
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
)

const (
	maxSize            = 4096 // cols/rows accepted
	defaultHistory     = 5000
	maxHistory         = 1000000
	killGrace          = 3 * time.Second // SIGHUP, then SIGKILL
	exitOutputDeadline = 500 * time.Millisecond
)

// pane is one program on a PTY. Fields are owned by the server loop unless
// noted.
type pane struct {
	id      int
	argv    []string
	dir     string
	created time.Time

	ptmx *os.File
	cmd  *exec.Cmd
	pid  int
	emu  *vt.Emulator
	in   *inputQueue

	cols, rows int
	title      string
	modes      map[modeKey]bool // modes the program set (for snapshots)
	cursorOn   bool
	cursorSt   int // DECSCUSR parameter (0 = terminal default)

	clients map[*client]bool // attached clients
	// muted is set while clients are attached: their emulators answer the
	// program's queries, so the server's emulator must not (read by the
	// reply goroutine).
	muted atomic.Bool

	dead   bool // exited while nobody was attached; kept until attached or killed
	status int
	closed bool
	killed bool // kill requested: forget it when it exits
	// exited is set by the wait goroutine once the process is reaped, so
	// the kill timer never signals a pid that may have been reused.
	exited atomic.Bool

	bytesIn, bytesOut int64
}

type modeKey struct {
	dec bool
	n   int
}

// paneEvent carries a pane's output, or its exit, to the server loop. Both
// go through one channel, so the exit always comes after all the output.
type paneEvent struct {
	p      *pane
	data   []byte
	exited bool
	status int
}

type spawnSpec struct {
	argv       []string
	env        []string
	dir        string
	cols, rows int
	history    int
}

func validateSpawn(r *Request) (spawnSpec, error) {
	sp := spawnSpec{argv: r.Argv, env: r.Env, dir: r.Dir, cols: r.Cols, rows: r.Rows, history: r.History}
	if len(sp.argv) == 0 || sp.argv[0] == "" {
		return sp, errors.New("spawn: argv is empty")
	}
	for _, a := range sp.argv {
		if strings.IndexByte(a, 0) >= 0 {
			return sp, errors.New("spawn: argv contains a NUL byte")
		}
	}
	for _, kv := range sp.env {
		if !strings.Contains(kv, "=") || strings.IndexByte(kv, 0) >= 0 {
			return sp, fmt.Errorf("spawn: bad environment entry %q", kv)
		}
	}
	if sp.dir != "" && !filepath.IsAbs(sp.dir) {
		return sp, fmt.Errorf("spawn: dir %q is not absolute", sp.dir)
	}
	if err := checkSize(sp.cols, sp.rows); err != nil {
		return sp, err
	}
	switch {
	case sp.history == 0:
		sp.history = defaultHistory
	case sp.history < 0:
		sp.history = 0
	case sp.history > maxHistory:
		sp.history = maxHistory
	}
	return sp, nil
}

func checkSize(cols, rows int) error {
	if cols < 1 || rows < 1 || cols > maxSize || rows > maxSize {
		return fmt.Errorf("bad size %dx%d", cols, rows)
	}
	return nil
}

// spawn starts the program on a new PTY: the child gets a new session with
// the PTY as its controlling terminal (pty.Start sets Setsid and Setctty),
// and the size is set before it runs.
func (s *Server) spawn(sp spawnSpec) (*pane, error) {
	s.nextPane++
	p := &pane{
		id: s.nextPane, argv: sp.argv, dir: sp.dir, created: time.Now(),
		cols: sp.cols, rows: sp.rows, modes: map[modeKey]bool{}, cursorOn: true,
		clients: map[*client]bool{},
	}
	// A bare program name is looked up in the PATH the client sent, not the
	// server's own (under systemd the server's PATH is minimal).
	path, err := lookPath(sp.argv[0], sp.env)
	if err != nil {
		return nil, fmt.Errorf("spawn: %w", err)
	}
	cmd := exec.Command(path, sp.argv[1:]...)
	cmd.Args[0] = sp.argv[0]
	cmd.Env = sp.env
	cmd.Dir = sp.dir
	if cmd.Dir == "" {
		cmd.Dir = "/"
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(sp.cols), Rows: uint16(sp.rows)})
	if err != nil {
		return nil, fmt.Errorf("spawn %q: %w", sp.argv[0], err)
	}
	p.ptmx, p.cmd, p.pid = pollable(ptmx), cmd, cmd.Process.Pid
	log := s.log.With("pane", p.id, "pid", p.pid)

	p.emu = vt.NewEmulator(sp.cols, sp.rows)
	p.emu.SetScrollbackSize(sp.history)
	p.emu.SetLogger(vtLogger{log})
	p.emu.SetCallbacks(vt.Callbacks{
		Title:            func(t string) { p.title = t },
		CursorVisibility: func(v bool) { p.cursorOn = v },
		CursorStyle: func(st vt.CursorStyle, steady bool) {
			// DECSCUSR: 1 blinking block, 2 steady block, 3/4 underline, 5/6 bar.
			n := int(st)*2 + 1
			if steady {
				n++
			}
			p.cursorSt = n
		},
		EnableMode:  func(m ansi.Mode) { p.setMode(m, true) },
		DisableMode: func(m ansi.Mode) { p.setMode(m, false) },
	})

	// Replies from the server's emulator (DA, DSR, ...) must always be read,
	// or the emulator blocks; they reach the program only while no client
	// is attached.
	p.in = newInputQueue()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := p.emu.Read(buf)
			if n > 0 && !p.muted.Load() {
				_, _ = p.in.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	go p.in.drainTo(p.ptmx, log)

	// Read the PTY all the time, attached or not: a PTY nobody reads fills
	// up and blocks the program.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := p.ptmx.Read(buf)
			if n > 0 {
				s.paneEvents <- paneEvent{p: p, data: append([]byte(nil), buf[:n]...)}
			}
			if err != nil {
				// EIO: the other side closed (program exited); ErrClosed: we
				// closed it.
				if !errors.Is(err, io.EOF) && !errors.Is(err, syscall.EIO) && !errors.Is(err, os.ErrClosed) {
					log.Warn("pty read failed", "err", err)
				}
				return
			}
		}
	}()
	// Reap the child, then report the exit after its remaining output (a
	// background process that keeps the PTY open must not hold it forever).
	go func() {
		err := cmd.Wait()
		p.exited.Store(true)
		status := exitStatus(err)
		select {
		case <-readDone:
		case <-time.After(exitOutputDeadline):
			log.Debug("output still open after exit (a child keeps the pty); not waiting")
		}
		s.paneEvents <- paneEvent{p: p, exited: true, status: status}
	}()
	return p, nil
}

func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return -1
}

func (p *pane) setMode(m ansi.Mode, on bool) {
	_, dec := m.(ansi.DECMode)
	p.modes[modeKey{dec: dec, n: m.Mode()}] = on
}

// feed passes output to the emulator. A panic in the emulator is logged
// and resets that pane's terminal instead of ending the server.
func (s *Server) feed(p *pane, data []byte) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("emulator panic; pane terminal reset", "pane", p.id, "panic", fmt.Sprint(r),
				"stack", string(debug.Stack()), "input_excerpt", fmt.Sprintf("%q", excerpt(data, 256)))
			func() {
				defer func() { _ = recover() }()
				_, _ = p.emu.Write([]byte(ansi.ResetInitialState))
			}()
		}
	}()
	_, _ = p.emu.Write(data)
}

func excerpt(b []byte, n int) []byte {
	if len(b) > n {
		return b[len(b)-n:]
	}
	return b
}

func (p *pane) resize(cols, rows int) {
	if cols == p.cols && rows == p.rows {
		return
	}
	p.cols, p.rows = cols, rows
	p.emu.Resize(cols, rows)
	if !p.closed {
		setWinsize(p.ptmx, cols, rows) // the kernel sends SIGWINCH
	}
}

// close releases the PTY and hangs up the program's process group; one
// that ignores SIGHUP gets SIGKILL after killGrace. The wait goroutine
// still reaps it.
func (p *pane) close(log interface{ Info(string, ...any) }) {
	if p.closed {
		return
	}
	p.closed = true
	p.in.Close()
	// End the reply goroutine by closing the emulator's reply pipe (not
	// emu.Close, whose flag is read without synchronization).
	if pw, ok := p.emu.InputPipe().(*io.PipeWriter); ok {
		_ = pw.CloseWithError(io.EOF)
	}
	_ = p.ptmx.Close()
	if !p.exited.Load() {
		pid := p.pid
		_ = syscall.Kill(-pid, syscall.SIGHUP)
		_ = syscall.Kill(pid, syscall.SIGHUP)
		time.AfterFunc(killGrace, func() {
			if !p.exited.Load() {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		})
		log.Info("pane hung up", "pane", p.id, "pid", pid)
	}
}

func (p *pane) info() PaneInfo {
	pi := PaneInfo{
		ID: p.id, PID: p.pid, Argv: p.argv, Dir: p.dir, Title: p.title,
		Cols: p.cols, Rows: p.rows, Attached: len(p.clients),
		Dead: p.dead, Status: p.status, Created: p.created.Unix(),
		Command: filepath.Base(p.argv[0]),
	}
	if !p.closed {
		fg := foregroundPgrp(p.ptmx)
		if fg <= 0 {
			fg = p.pid
		}
		if n := processName(fg); n != "" {
			pi.Command = n
		}
		pi.Cwd = processDir(fg)
		if pi.Cwd == "" {
			pi.Cwd = processDir(p.pid)
		}
	}
	return pi
}

// snapshot returns escape sequences that recreate the pane in a fresh
// terminal emulator of p.cols×p.rows: the last `history` scrollback lines
// (-1 = all), the screen, the modes the program set, the title, and the
// cursor.
//
// Limits: while a full-screen program uses the alternate screen, the main
// screen underneath it is not included (the emulator doesn't expose it),
// and the scroll region is not restored (full-screen programs set it again
// on their next redraw).
func (p *pane) snapshot(history int) []byte {
	var b strings.Builder
	b.WriteString("\x1b[0m\x1b[H\x1b[2J")
	sb := p.emu.ScrollbackLen()
	if history < 0 || history > sb {
		history = sb
	}
	for i := sb - history; i < sb; i++ {
		b.WriteString(renderLine(p.emu.Scrollback().Line(i)))
		b.WriteString("\x1b[0m\r\n")
	}
	w, h := p.emu.Width(), p.emu.Height()
	row := func(y int) uv.Line {
		line := uv.NewLine(w)
		for x := 0; x < w; x++ {
			if c := p.emu.CellAt(x, y); c != nil {
				line[x] = *c
			}
		}
		return line
	}
	alt := p.emu.IsAltScreen()
	if alt {
		// Move the history into the scrollback, then switch.
		for y := 0; y < h-1; y++ {
			b.WriteString("\r\n")
		}
		b.WriteString("\x1b[?1049h\x1b[H")
		for y := 0; y < h; y++ {
			fmt.Fprintf(&b, "\x1b[%dH%s\x1b[0m", y+1, renderLine(row(y)))
		}
	} else {
		for y := 0; y < h; y++ {
			if y > 0 {
				b.WriteString("\r\n")
			}
			b.WriteString(renderLine(row(y)))
			b.WriteString("\x1b[0m")
		}
	}
	// Modes. The alternate screen was handled above; the cursor and
	// autowrap are written only when changed from the default.
	for k, on := range p.modes {
		if !on {
			continue
		}
		if k.dec {
			switch k.n {
			case 7, 25, 47, 1047, 1048, 1049, 6, 2026:
				continue
			}
			fmt.Fprintf(&b, "\x1b[?%dh", k.n)
		} else {
			fmt.Fprintf(&b, "\x1b[%dh", k.n)
		}
	}
	if on, ok := p.modes[modeKey{dec: true, n: 7}]; ok && !on {
		b.WriteString("\x1b[?7l")
	}
	if p.cursorSt != 0 {
		fmt.Fprintf(&b, "\x1b[%d q", p.cursorSt)
	}
	if p.title != "" {
		b.WriteString(ansi.SetWindowTitle(sanitizeTitle(p.title)))
	}
	cur := p.emu.CursorPosition()
	fmt.Fprintf(&b, "\x1b[%d;%dH", cur.Y+1, cur.X+1)
	if !p.cursorOn {
		b.WriteString("\x1b[?25l")
	}
	return []byte(b.String())
}

func renderLine(l uv.Line) string {
	if l == nil {
		return ""
	}
	return uv.TrimSpace(l.Render())
}

// sanitizeTitle drops control characters, so a title can't end the OSC
// sequence early.
func sanitizeTitle(t string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, t)
}

// vtLogger routes the emulator's own diagnostics (unhandled sequences) to
// the debug log.
type vtLogger struct {
	log interface{ Debug(string, ...any) }
}

func (l vtLogger) Printf(format string, v ...any) {
	l.log.Debug("emulator: " + fmt.Sprintf(format, v...))
}

// inputQueue is an unbounded, non-blocking byte queue drained into the
// PTY by its own goroutine, so a program that doesn't read its input never
// blocks the server.
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
	q.buf = nil
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *inputQueue) drainTo(dst io.Writer, log interface{ Debug(string, ...any) }) {
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
			log.Debug("pty write stopped", "err", err)
			return
		}
	}
}

// lookPath finds an executable like a shell would, using the PATH in env
// (or a default PATH if env has none). A name containing "/" is used as is.
func lookPath(name string, env []string) (string, error) {
	if strings.Contains(name, "/") {
		if err := isExecutable(name); err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		return name, nil
	}
	path := "/usr/local/bin:/usr/bin:/bin"
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue // relative PATH entries would depend on the server's cwd
		}
		p := filepath.Join(dir, name)
		if isExecutable(p) == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%q not found in PATH %q", name, path)
}

func isExecutable(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return errors.New("is a directory")
	}
	if fi.Mode()&0o111 == 0 {
		return errors.New("not executable")
	}
	return nil
}
