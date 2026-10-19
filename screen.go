package main

import (
	"bytes"
	"fmt"
	"image/color"
	"io"

	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// hostScreen draws frames to the host terminal.
//
// It drives ultraviolet's diffing renderer directly instead of using
// uv.TerminalScreen, which queues each frame's final cursor move in a buffer
// it has already flushed. Working around that took two writes per frame, and
// between them the real cursor was visible wherever drawing stopped, often in
// another window, so it seemed to blink there. Here every frame is one write:
// cells, then the cursor move, then its visibility and shape, wrapped in
// synchronized output (mode 2026) so terminals that support it update
// atomically.
type hostScreen struct {
	out   io.Writer
	win   *uv.Window       // this frame, drawn from scratch
	rbuf  *uv.RenderBuffer // what the renderer diffs against
	rend  *uv.TerminalRenderer
	frame bytes.Buffer // renderer output for one frame

	cursorShown bool
	shape       uv.CursorShape
	blink       bool
	cursorColor string // "#rrggbb" last set with OSC 12; "" = terminal default

	mouse    bool // mouse reporting is on (setup)
	keyboard bool // enhanced key reports asked for (keyboard_enhancements; keyboard.go)
	repaint  bool // the next frame rewrites everything (see repaintAll)
}

func newHostScreen(out io.Writer, env []string, w, h int) *hostScreen {
	s := &hostScreen{out: out, shape: uv.CursorBlock, blink: true}
	s.rend = uv.NewTerminalRenderer(&s.frame, env)
	s.rend.SetColorProfile(colorprofile.Detect(out, env)) // (run sets the one true_color decides)
	s.rend.SetFullscreen(true)
	s.rend.SetRelativeCursor(false)
	s.win = uv.NewScreen(w, h)
	s.rbuf = uv.NewRenderBuffer(w, h)
	s.resize(w, h)
	return s
}

func (s *hostScreen) resize(w, h int) {
	s.win.Resize(w, h)
	s.rbuf.Resize(w, h)
	s.rbuf.Touched = nil
	s.rend.Resize(w, h)
	s.rend.Erase() // full redraw on the next frame
}

// setup switches the terminal into goat's mode: alternate screen, hidden
// cursor, and the modes of writeModes.
func (s *hostScreen) setup(mouse bool) error {
	var b bytes.Buffer
	s.mouse = mouse
	b.WriteString(ansi.SetModeAltScreenSaveCursor + ansi.HideCursor)
	if s.keyboard {
		b.WriteString("\x1b[>5u") // a kitty keyboard entry of goat's own (popped at teardown)
	}
	s.writeModes(&b)
	// Ask for the host's default colors (see WM.setHostColors), its cell
	// size in pixels, and to be told when it turns dark or light (mode
	// 2031); programs in windows asking for these get them
	// (termreplies.go). Terminals that don't know them say nothing.
	b.WriteString(ansi.RequestForegroundColor + ansi.RequestBackgroundColor)
	b.WriteString("\x1b[16t" + "\x1b[?2031h" + "\x1b[?996n")
	_, err := s.out.Write(b.Bytes())
	return err
}

// askCellSize asks the terminal for its cell size in pixels again (CSI 16
// t; the answer comes as a CellSizeEvent).
func (s *hostScreen) askCellSize() { _, _ = io.WriteString(s.out, "\x1b[16t") }

// teardown undoes setup. The cursor is always shown and its shape reset to
// the terminal's default, whatever the programs in the windows did to it.
func (s *hostScreen) teardown(mouse bool) {
	var b bytes.Buffer
	if mouse {
		_ = uv.EncodeMouseMode(&b, uv.MouseModeNone)
	}
	b.WriteString(ansi.ResetModeFocusEvent + ansi.ResetModeBiDirectionalSupport + "\x1b[?2031l")
	if s.keyboard {
		b.WriteString("\x1b[<u" + "\x1b[>4m") // the keyboard as it was
	}
	if s.cursorColor != "" {
		b.WriteString(ansi.ResetCursorColor)
	}
	b.WriteString(ansi.ResetModeBracketedPaste + ansi.ResetModeAltScreenSaveCursor +
		ansi.ShowCursor + ansi.SetCursorStyle(0))
	_, _ = s.out.Write(b.Bytes())
}

// writeModes writes the terminal modes goat runs in:
//
//   - bracketed paste, and mouse reporting unless disable_mouse is set;
//   - focus reporting (1004): goat repaints the screen when the terminal
//     gets focus back, in case something else wrote to it meanwhile;
//   - no bidirectional reordering (ECMA-48 BDSM reset to explicit, CSI 8 h).
//     Terminals that reorder right-to-left text by themselves (GNOME
//     Terminal and other VTE ones) would otherwise move the characters of a
//     line around, window borders included. Others ignore it.
//
// They are written again with every repaint, in case something reset them.
func (s *hostScreen) writeModes(b *bytes.Buffer) {
	b.WriteString(ansi.SetModeBracketedPaste + ansi.SetModeFocusEvent + ansi.SetModeBiDirectionalSupport)
	if s.keyboard {
		// Kitty keyboard flags 1 + 4 (disambiguate, alternate keys) on the
		// entry setup pushed (repeating this changes nothing), and
		// modifyOtherKeys 2 for terminals with only that (keyboard.go).
		b.WriteString("\x1b[=5;1u" + "\x1b[>4;2m")
	}
	if s.mouse {
		_ = uv.EncodeMouseMode(b, uv.MouseModeDrag)
	}
}

// repaintAll makes the next frame rewrite every cell, in place (so nothing
// flickers), after putting back the terminal state goat relies on: the
// character set, scroll region, autowrap and other modes, colors, and the
// cursor's shape, color and visibility. It repairs whatever made the screen
// differ from what goat drew: a character the terminal drew wider than goat
// expected, output from a program writing to the terminal directly, the
// terminal reset, ...
func (s *hostScreen) repaintAll() {
	s.repaint = true
}

// cursorState is where (and how) the host cursor should be after a frame.
type cursorState struct {
	visible bool
	x, y    int
	shape   uv.CursorShape
	blink   bool
	color   color.Color // nil = the terminal's own cursor color
}

// hexColor is c as "#rrggbb" ("" for nil).
func hexColor(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// render draws d and positions the cursor, in a single write.
func (s *hostScreen) render(d uv.Drawable, cur cursorState) error {
	s.win.Clear()
	d.Draw(s.win, s.win.Bounds())
	for y := range s.win.Height() {
		for x := 0; x < s.win.Width(); {
			c := s.win.CellAt(x, y)
			if c == nil || c.IsZero() { // second half of a wide character
				x++
				continue
			}
			s.rbuf.SetCell(x, y, c)
			x += max(c.Width, 1)
		}
	}
	s.frame.Reset()
	repaint := s.repaint
	if repaint {
		s.repaint = false
		s.writeModes(&s.frame)
		s.rend.Invalidate(s.rbuf)
		// Not known any more: sent again below.
		s.cursorShown = !cur.visible
		s.shape, s.cursorColor = -1, "?"
	}
	s.rend.Render(s.rbuf)
	cells := s.rend.Buffered() > 0 || repaint
	if cur.visible {
		s.rend.MoveTo(cur.x, cur.y)
	}
	if err := s.rend.Flush(); err != nil {
		return err
	}

	var b bytes.Buffer
	if cells {
		b.WriteString(ansi.SetModeSynchronizedOutput)
		if s.cursorShown { // don't let the cursor be seen travelling
			b.WriteString(ansi.HideCursor)
			s.cursorShown = false
		}
	}
	b.Write(s.frame.Bytes())
	if cur.visible {
		if cur.shape != s.shape || cur.blink != s.blink {
			s.shape, s.blink = cur.shape, cur.blink
			_ = uv.EncodeCursorStyle(&b, s.shape, s.blink)
		}
		// Cursor color (OSC 12): the focused window's text color, or the
		// terminal's default (OSC 112) for windows without one.
		if want := hexColor(cur.color); want != s.cursorColor {
			s.cursorColor = want
			if want == "" {
				b.WriteString(ansi.ResetCursorColor)
			} else {
				b.WriteString(ansi.SetCursorColor(want))
			}
		}
		if !s.cursorShown {
			b.WriteString(ansi.ShowCursor)
			s.cursorShown = true
		}
	} else if s.cursorShown {
		b.WriteString(ansi.HideCursor)
		s.cursorShown = false
	}
	if cells {
		b.WriteString(ansi.ResetModeSynchronizedOutput)
	}
	if b.Len() == 0 {
		return nil
	}
	_, err := s.out.Write(b.Bytes())
	return err
}
