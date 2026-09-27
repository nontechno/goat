package main

import (
	"bytes"
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
}

func newHostScreen(out io.Writer, env []string, w, h int) *hostScreen {
	s := &hostScreen{out: out, shape: uv.CursorBlock, blink: true}
	s.rend = uv.NewTerminalRenderer(&s.frame, env)
	s.rend.SetColorProfile(colorprofile.Detect(out, env))
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

// setup switches the terminal into gloat's mode: alternate screen, hidden
// cursor, bracketed paste, optional mouse reporting.
func (s *hostScreen) setup(mouse bool) error {
	var b bytes.Buffer
	b.WriteString(ansi.SetModeAltScreenSaveCursor + ansi.HideCursor + ansi.SetModeBracketedPaste)
	if mouse {
		_ = uv.EncodeMouseMode(&b, uv.MouseModeDrag)
	}
	// Ask for the host's default colors (see WM.setHostColors).
	b.WriteString(ansi.RequestForegroundColor + ansi.RequestBackgroundColor)
	_, err := s.out.Write(b.Bytes())
	return err
}

// teardown undoes setup. The cursor is always shown and its shape reset to
// the terminal's default, whatever the programs in the windows did to it.
func (s *hostScreen) teardown(mouse bool) {
	var b bytes.Buffer
	if mouse {
		_ = uv.EncodeMouseMode(&b, uv.MouseModeNone)
	}
	b.WriteString(ansi.ResetModeBracketedPaste + ansi.ResetModeAltScreenSaveCursor +
		ansi.ShowCursor + ansi.SetCursorStyle(0))
	_, _ = s.out.Write(b.Bytes())
}

// cursorState is where (and how) the host cursor should be after a frame.
type cursorState struct {
	visible bool
	x, y    int
	shape   uv.CursorShape
	blink   bool
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
	s.rend.Render(s.rbuf)
	cells := s.rend.Buffered() > 0
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
