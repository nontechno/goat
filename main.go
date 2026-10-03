// Command goat is a floating-window terminal multiplexer: freely positioned,
// overlapping terminal windows driven by keyboard and mouse.
//
// It is a Go port of float (github.com/Henktorius/float), built on
// charmbracelet/x/vt for terminal emulation and charmbracelet/ultraviolet for
// the host terminal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"os"
	"os/signal"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

var version = "dev"

const (
	// keySeqTimeout is how long the input decoder waits to see whether an
	// ESC starts an escape sequence. Short, so that a human pressing Esc and
	// then an arrow is not merged into Alt+arrow.
	keySeqTimeout = 30 * time.Millisecond
	titleInterval = 500 * time.Millisecond
	frameInterval = 16 * time.Millisecond // at most ~60 redraws per second
	// parseBudget caps how long program output is fed to the emulators
	// before input is looked at and a frame drawn, so a flood of output
	// (cat of a huge file) can't freeze keys, mouse or the screen.
	parseBudget = 8 * time.Millisecond
)

func main() {
	cfgPath := flag.String("config", "", "config file (default: ~/.config/goat/config.toml)")
	showVersion := flag.Bool("version", false, "print version and exit")
	cpuProfile := flag.String("cpuprofile", "", "write a CPU profile to this file (for diagnosing)")
	keys := flag.Bool("keys", false, "show what each key press sends and what goat makes of it, then exit (Ctrl+C)")
	flag.Parse()
	if *cpuProfile != "" {
		if f, err := os.Create(*cpuProfile); err == nil {
			_ = pprof.StartCPUProfile(f)
			defer func() { pprof.StopCPUProfile(); _ = f.Close() }()
		}
	}
	if *showVersion {
		fmt.Println("goat", version)
		return
	}

	cfg, usedPath, warns := LoadConfig(*cfgPath)
	if *keys {
		if err := keyTest(cfg, usedPath, warns, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "goat:", err)
			os.Exit(1)
		}
		return
	}
	if w, closeLog := setupLog(cfg); true {
		defer closeLog()
		if w != "" {
			warns = append(warns, w)
		}
	}
	err := run(cfg, usedPath, warns)
	// Printed after the full-screen UI is gone, so they stay visible.
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "goat: warning:", w)
	}
	if err != nil {
		logger.Error("exit", "err", err)
		fmt.Fprintln(os.Stderr, "goat:", err)
		pprof.StopCPUProfile()
		os.Exit(1)
	}
	logger.Info("exit")
}

// crashState describes goat's state for a crash report (set once the window
// manager exists).
var crashState = func() string { return "" }

// reportCrash logs a panic in the main loop with its stack and goat's state,
// after which the caller restores the terminal and tells the user.
func reportCrash(r any) {
	stack := string(debug.Stack())
	state := func() (s string) {
		defer func() {
			if e := recover(); e != nil {
				s = fmt.Sprintf("(state unavailable: %v)", e)
			}
		}()
		return crashState()
	}()
	logger.Error("crash", "panic", fmt.Sprint(r), "state", state, "stack", stack)
}

func run(cfg *Config, cfgPath string, warns []string) (err error) {
	in, out := os.Stdin, os.Stdout
	if !term.IsTerminal(in.Fd()) || !term.IsTerminal(out.Fd()) {
		return errors.New("stdin and stdout must be a terminal")
	}
	state, err := term.MakeRaw(in.Fd())
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	cols, rows, gerr := term.GetSize(out.Fd())
	if gerr != nil || cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	scr := newHostScreen(out, os.Environ(), cols, rows)
	mouse := !cfg.DisableMouse
	logSessionStart(cfgPath, cols, rows)
	for _, w := range warns {
		logger.Warn("config", "problem", w, "file", cfgPath)
	}

	// Always give the user their terminal back, even on a panic.
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		scr.teardown(mouse)
		_ = term.Restore(in.Fd(), state)
	}
	defer func() {
		if r := recover(); r != nil {
			reportCrash(r)
			restore()
			fmt.Fprintf(os.Stderr, "goat crashed: %v\n", r)
			if logPath != "" {
				fmt.Fprintf(os.Stderr, "details (stack trace, window state) are in %s\n", logPath)
			} else {
				fmt.Fprintf(os.Stderr, "%s\n", debug.Stack())
			}
			os.Exit(2)
		}
		restore()
	}()
	if err := scr.setup(mouse); err != nil {
		return err
	}

	// Input. uv.TerminalReader decodes keys, mouse, paste and replies; it
	// reads into a fresh buffer each time. (uv.Terminal is not used: its
	// input loop reuses one buffer across goroutines, a data race.)
	events := make(chan uv.Event, 64)
	rd := uv.NewTerminalReader(in, os.Getenv("TERM"))
	rd.EscTimeout = keySeqTimeout
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = rd.StreamEvents(ctx, events) }()

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	m := newWM(cfg, warns, cols, rows)
	if cfg.ShowHost || cfg.ShowUser {
		home := m.home
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		var source string
		m.host, source = hostLabel(home)
		m.user = currentUserName()
		m.hostChecked = time.Now()
		m.identOn = true
		logger.Info("identity for window frames", "user", m.user, "host", m.host, "host_source", source,
			"show_user", cfg.ShowUser, "show_host", cfg.ShowHost)
	}
	m.dir, _ = os.Getwd()
	crashState = m.snapshot
	if err := m.startBackground(); err != nil {
		return fmt.Errorf("starting shell %q: %w", m.shell(), err)
	}
	for _, w := range warns {
		m.logEvent(nil, evConfig, w)
	}
	if len(warns) > 0 {
		msg := "config: " + warns[0]
		if len(warns) > 1 {
			msg += fmt.Sprintf(" (+%d more, listed when goat exits)", len(warns)-1)
		}
		m.setStatus(msg, 20*time.Second)
	}
	copyCmd := clipboardCommand(cfg.CopyCommand, realClipboardEnv())
	copyErrs := make(chan string, 4)
	m.copy = func(text, who string) {
		var via []string
		if cfg.ClipboardOSC52 {
			_, _ = out.WriteString(ansi.SetSystemClipboard(text))
			via = append(via, "terminal")
		}
		if copyCmd != nil {
			runClipboardCommand(copyCmd, text, func(msg string) {
				select {
				case copyErrs <- msg:
				default:
				}
			})
			via = append(via, copyCmd[0])
		}
		msg := fmt.Sprintf("copied %d characters (via %s)", utf8.RuneCountInString(text), strings.Join(via, " + "))
		if who != "" {
			msg = who + " " + msg
		}
		if len(via) == 0 {
			msg = "nothing to copy with: set clipboard_osc52 = true or copy_command"
		}
		m.setStatus(msg, 4*time.Second)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	titles := time.NewTicker(titleInterval)
	defer titles.Stop()
	frame := make(chan struct{}, 1)
	var lastFrame time.Time
	frameArmed := false

	handle := func(ev uv.Event) {
		if e := m.handleEvent(ev, scr); e != nil {
			logger.Warn("event", "err", e, "event", fmt.Sprintf("%T", ev))
			m.setStatus(e.Error(), 10*time.Second)
			m.logEvent(nil, evError, e.Error())
		}
	}

	for !m.quit {
		select {
		case ev := <-events:
			handle(ev)
		case msg := <-m.out:
			m.handlePty(msg)
		case msg := <-m.identOut:
			m.handleIdent(msg)
		case <-m.escFire:
			m.escExpired()
		case msg := <-copyErrs:
			logger.Warn("clipboard", "err", msg, "command", strings.Join(copyCmd, " "))
			m.setStatus(msg, 8*time.Second)
			m.logEvent(nil, evError, "clipboard: "+msg)
		case now := <-titles.C:
			m.tick(now)
		case <-frame:
			frameArmed = false
		case <-winch:
			if c, h, err := term.GetSize(out.Fd()); err == nil && c > 0 && h > 0 {
				handle(uv.WindowSizeEvent{Width: c, Height: h})
			}
		case s := <-sigs:
			logger.Info("signal", "signal", s.String())
			m.quit = true
		}
		// Work through what else is waiting, input first, spending at most
		// parseBudget on program output before drawing.
		deadline := time.Now().Add(parseBudget)
	drain:
		for !m.quit {
			select {
			case ev := <-events:
				handle(ev)
				continue
			default:
			}
			if time.Now().After(deadline) {
				break drain
			}
			select {
			case msg := <-m.out:
				m.handlePty(msg)
			default:
				break drain
			}
		}
		if !m.dirty || m.quit {
			continue
		}
		// Draw when a frame is due. Checked on every turn (not only via the
		// timer), so a steady stream of output can't postpone frames; the
		// timer only matters when goat would otherwise go idle.
		if wait := frameInterval - time.Since(lastFrame); wait > 0 {
			if !frameArmed {
				frameArmed = true
				time.AfterFunc(wait, func() { frame <- struct{}{} })
			}
			continue
		}
		if err := m.render(scr); err != nil {
			m.closeAll()
			return fmt.Errorf("writing to the terminal: %w", err)
		}
		lastFrame = time.Now()
	}
	m.closeAll()
	return nil
}

// handleEvent dispatches one host terminal event.
func (m *WM) handleEvent(ev uv.Event, scr *hostScreen) error {
	switch ev := ev.(type) {
	case uv.WindowSizeEvent:
		scr.resize(ev.Width, ev.Height)
		m.resizeScreen(ev.Width, ev.Height)
	case uv.KeyPressEvent:
		return m.handleKey(uv.Key(ev))
	// The reader sends PasteStart, PasteEvent (the text), PasteEnd. Keys that
	// arrive between start and end (from decoders that don't collect the
	// text themselves) are gathered and pasted at the end.
	case uv.PasteStartEvent:
		m.pasting, m.pasteBuf = true, nil
	case uv.PasteEndEvent:
		m.pasting = false
		m.paste(string(m.pasteBuf))
		m.pasteBuf = nil
	case uv.PasteEvent:
		m.paste(ev.Content)
	case uv.MouseClickEvent:
		m.handleMouse(ev)
	case uv.MouseReleaseEvent:
		m.handleMouse(ev)
	case uv.MouseMotionEvent:
		m.handleMouse(ev)
	case uv.MouseWheelEvent:
		m.handleMouse(ev)
	case uv.ForegroundColorEvent:
		m.setHostColors(ev.Color, nil)
	case uv.BackgroundColorEvent:
		m.setHostColors(nil, ev.Color)
	}
	return nil
}

// setHostColors records the host terminal's default colors and passes them to
// every emulator (they are what programs get when they query the colors).
func (m *WM) setHostColors(fg, bg color.Color) {
	if fg != nil {
		m.hostFg = fg
	}
	if bg != nil {
		m.hostBg = bg
	}
	for _, w := range m.windows {
		m.applyHostColors(w)
	}
}

func (m *WM) applyHostColors(w *Window) {
	switch {
	case w.fgColor != nil: // chosen with Alt+b
		w.emu.SetDefaultForegroundColor(w.fgColor)
	case m.hostFg != nil:
		w.emu.SetDefaultForegroundColor(m.hostFg)
	}
	switch {
	case w.bgColor != nil: // chosen with Alt+b
		w.emu.SetDefaultBackgroundColor(w.bgColor)
	case m.hostBg != nil:
		w.emu.SetDefaultBackgroundColor(m.hostBg)
	}
}

// render draws one frame, with the host cursor at the focused program's
// cursor (or hidden).
func (m *WM) render(scr *hostScreen) error {
	if f := m.focused; f != nil {
		f.followCursorView() // no-wrap: keep what is being typed in view
	}
	var cur cursorState
	if x, y, ok := m.cursorFor(); ok {
		w := m.focused
		cur = cursorState{visible: true, x: x, y: y, shape: w.cursorShape, blink: w.cursorBlink,
			color: w.fgColor} // the window's Alt+b text color, if any
	}
	m.dirty = false
	return scr.render(scene{m}, cur)
}
