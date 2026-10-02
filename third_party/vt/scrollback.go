package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

// DefaultScrollbackSize is the default maximum number of lines in the scrollback buffer.
const DefaultScrollbackSize = 10000

// Scrollback represents a scrollback buffer that stores lines scrolled off the screen.
//
// GOAT PATCH: the lines are kept in a ring. Once the buffer is full, pushing
// a line reuses the storage of the line it evicts, instead of deleting the
// first element (which moved every line header, O(maxLines) per push) and
// allocating a fresh copy of the new line. Under a flood of output this was a
// third of the emulator's time. A consequence: a cell pointer or line slice
// obtained from the buffer is only valid until the next Push.
type Scrollback struct {
	ring     []uv.Line // len(ring) == number of lines held
	start    int       // index in ring of the oldest line
	maxLines int
	pushed   int // lines ever pushed (GOAT PATCH, see Pushed)

	// onEvict, if set, sees each line as it is dropped to make room (GOAT
	// PATCH, see SetEvictHandler).
	onEvict func(n int, line uv.Line)
}

// SetEvictHandler sets a function called with each line that is about to
// be dropped because the buffer is full (or made smaller), oldest first
// (GOAT PATCH). n is the line's number (see Pushed: the n-th line ever
// pushed, from 0). The line is only valid during the call. Lines removed by
// Clear are not passed to it.
func (s *Scrollback) SetEvictHandler(fn func(n int, line uv.Line)) {
	if s != nil {
		s.onEvict = fn
	}
}

// Pushed returns how many lines were ever pushed, including those since
// dropped (GOAT PATCH). Pushed()-Len() is the number of lines that were
// dropped or cleared, so a line keeps the number Pushed()-Len()+i while it is
// in the buffer, even once the buffer is full and every push drops one.
func (s *Scrollback) Pushed() int {
	if s == nil {
		return 0
	}
	return s.pushed
}

// NewScrollback creates a new scrollback buffer with the given maximum number of lines.
func NewScrollback(maxLines int) *Scrollback {
	if maxLines <= 0 {
		maxLines = DefaultScrollbackSize
	}
	return &Scrollback{
		ring:     make([]uv.Line, 0, min(maxLines, 1000)), // Pre-allocate reasonable capacity
		maxLines: maxLines,
	}
}

// isBlank reports whether c is a zero cell or equal to [uv.EmptyCell]: a
// single-width space with no style and no link. It is exactly
// c.IsZero() || c.Equal(&uv.EmptyCell), without the per-cell calls.
func isBlank(c *uv.Cell) bool {
	if c.Content == "" {
		return c.IsZero()
	}
	st := &c.Style
	return c.Content == " " && c.Width == 1 &&
		st.Fg == nil && st.Bg == nil && st.UnderlineColor == nil &&
		st.Underline == 0 && st.Attrs == 0 &&
		c.Link.URL == "" && c.Link.Params == ""
}

// Push adds a line to the scrollback buffer.
// If the buffer is full, the oldest line is removed.
func (s *Scrollback) Push(line uv.Line) {
	if s == nil || s.maxLines <= 0 {
		return
	}

	// Find last non-empty cell to trim trailing empty cells.
	// This helps with wrapping and window resizing.
	n := len(line)
	for n > 0 && isBlank(&line[n-1]) {
		n--
	}
	line = line[:n]
	s.pushed++

	if len(s.ring) < s.maxLines {
		s.ring = append(s.ring, append(make(uv.Line, 0, n), line...))
		return
	}
	// Full: overwrite the oldest line, reusing its storage when the new line
	// fits it without much to spare. Otherwise allocate exactly: reusing any
	// larger slot, or letting append over-allocate, left the buffer holding
	// about twice the cells it used.
	old := s.ring[s.start]
	if s.onEvict != nil {
		s.onEvict(s.pushed-1-len(s.ring), old)
	}
	if cap(old) < n {
		old = make(uv.Line, 0, (n+15)&^15)
	}
	s.ring[s.start] = append(old[:0], line...)
	s.start++
	if s.start == len(s.ring) {
		s.start = 0
	}
}

// PushN adds n lines from the buffer starting at line y to the scrollback.
func (s *Scrollback) PushN(buf *uv.RenderBuffer, y, n int) {
	if s == nil || buf == nil || n <= 0 {
		return
	}

	for i := range min(n, buf.Height()-y) {
		if line := buf.Line(y + i); line != nil {
			s.Push(line)
		}
	}
}

// Len returns the number of lines in the scrollback buffer.
func (s *Scrollback) Len() int {
	if s == nil {
		return 0
	}
	return len(s.ring)
}

// MaxLines returns the maximum number of lines the scrollback buffer can hold.
func (s *Scrollback) MaxLines() int {
	if s == nil {
		return 0
	}
	return s.maxLines
}

// linearize rotates the ring so the oldest line is at index 0.
func (s *Scrollback) linearize() {
	if s.start == 0 {
		return
	}
	lines := make([]uv.Line, len(s.ring), cap(s.ring))
	n := copy(lines, s.ring[s.start:])
	copy(lines[n:], s.ring[:s.start])
	s.ring, s.start = lines, 0
}

// SetMaxLines sets the maximum number of lines in the scrollback buffer.
// If the current number of lines exceeds the new maximum, oldest lines are removed.
func (s *Scrollback) SetMaxLines(maxLines int) {
	if s == nil || maxLines <= 0 {
		return
	}

	s.linearize()
	s.maxLines = maxLines
	if len(s.ring) > maxLines {
		// Remove oldest lines
		if s.onEvict != nil {
			for i, l := range s.ring[:len(s.ring)-maxLines] {
				s.onEvict(s.pushed-len(s.ring)+i, l)
			}
		}
		s.ring = append([]uv.Line(nil), s.ring[len(s.ring)-maxLines:]...)
	}
}

// Line returns the line at the given index.
// Index 0 is the oldest line, Len()-1 is the most recent.
// Returns nil if index is out of bounds.
func (s *Scrollback) Line(index int) uv.Line {
	if s == nil || index < 0 || index >= len(s.ring) {
		return nil
	}
	i := s.start + index
	if i >= len(s.ring) {
		i -= len(s.ring)
	}
	return s.ring[i]
}

// Lines returns all lines in the scrollback buffer.
// Index 0 is the oldest line.
func (s *Scrollback) Lines() []uv.Line {
	if s == nil {
		return nil
	}
	s.linearize()
	return s.ring
}

// Clear removes all lines from the scrollback buffer.
func (s *Scrollback) Clear() {
	if s == nil {
		return
	}
	clear(s.ring) // let the old lines be collected
	s.ring, s.start = s.ring[:0], 0
}

// CellAt returns the cell at the given position in the scrollback buffer.
// x is the column, y is the line index (0 = oldest).
// Returns nil if position is out of bounds.
func (s *Scrollback) CellAt(x, y int) *uv.Cell {
	line := s.Line(y)
	if line == nil || x < 0 || x >= len(line) {
		return nil
	}
	return &line[x]
}
