package uv

// GOAT PATCH: what reaches the host terminal can't break the screen.
//
// Programs in goat's windows can print anything. goat's emulator turns that
// into cells, and this renderer writes the cells to the host terminal. Two
// things could still go wrong on the way, and both used to push text, window
// borders included, out of place until those cells happened to be redrawn:
//
// Characters the host acts on rather than shows. Bidi controls (U+202E,
// U+200F, ...) make some terminals reorder the line; C1 controls, line and
// paragraph separators and other format characters are interpreted, ignored
// or drawn, depending on the terminal. SafeContent removes them, and makes
// sure every cell starts with something that takes up room, before the cell
// is written. Hyperlink targets lose control characters too.
//
// Characters whose width terminals disagree on. The renderer moves the
// host's cursor by assuming that each character advances it by the cell's
// width. For most text that holds, but not for, for example:
//
//   - emoji presentation sequences such as "⚙️" (U+2699 U+FE0F): two columns
//     by Unicode, but macOS Terminal advances one (and draws the emoji wide);
//   - symbols, arrows and dingbats such as "■ ✔ ↩ ⚠": one column by Unicode,
//     two in terminals set to treat ambiguous characters as wide, or that
//     draw them as emoji;
//   - ZWJ sequences, flags, keycaps, skin tones, Indic conjuncts: anything
//     from one column to the number of code points;
//   - characters from scripts and blocks not listed in trustedRune, including
//     ones that don't exist yet: their width is a guess in every terminal.
//
// For a cell UncertainWidth reports, the renderer (putUncertainCell):
//
//  1. blanks the columns the cell owns (ECH), when it owns more than one, so
//     a terminal that advances less leaves no stale text behind;
//  2. writes the character, with autowrap off at the end of a line so a
//     terminal that thinks it wider can't wrap or scroll;
//  3. puts the cursor where it should be (CUP) instead of trusting the
//     terminal's;
//  4. for a one-column cell, redraws the cell to its right: a terminal that
//     drew the character two wide covered it, and the diff would otherwise
//     leave it covered because it didn't change.
//
// Text stays where goat put it; at worst a character drawn wider than its
// cell is cut off by its neighbor. Everything else is written as before.
//
// Finally, Invalidate lets goat repair whatever got past all this, or was
// written to the terminal by someone else: it redraws every cell in place.

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// plainASCII reports whether s is one printable ASCII character: the common
// case, written without further checks.
func plainASCII(s string) bool {
	return len(s) == 1 && s[0] >= 0x20 && s[0] < 0x7f
}

// UncertainWidth reports whether terminals may disagree about how many
// columns the grapheme cluster s takes. s should have passed SafeContent.
func UncertainWidth(s string) bool {
	if len(s) < 2 && (len(s) == 0 || s[0] < utf8.RuneSelf) {
		return false // empty or one ASCII byte: the common case
	}
	first := true
	for _, r := range s {
		if first {
			first = false
			if !trustedRune(r) {
				return true
			}
			continue
		}
		// What follows the first code point. Nonspacing marks (é as e +
		// U+0301) take no room anywhere; anything else may: variation
		// selectors (also marks, but they switch to emoji), joiners, skin
		// tones, flags, keycaps, spacing marks, the letters of a conjunct.
		if r >= 0xFE00 && r <= 0xFE0F || !unicode.Is(unicode.Mn, r) {
			return true
		}
	}
	return false
}

// trustedRune reports whether every terminal gives r the width Unicode
// does: the scripts and blocks whose width nobody disputes. Anything else is
// written with care (see above), which costs a few bytes, not correctness.
func trustedRune(r rune) bool {
	switch {
	case r < 0x0300:
		return r != 0xAD // Latin, Latin-1, IPA, spacing modifiers; not the soft hyphen
	case r < 0x0370:
		return false // combining marks with nothing to combine with
	case r < 0x0590:
		return true // Greek, Cyrillic, Armenian
	case r >= 0x0E01 && r <= 0x0E5B:
		return r != 0x0E33 // Thai (sara am is drawn as two)
	case r >= 0x10A0 && r <= 0x10FF:
		return true // Georgian
	case r >= 0x1E00 && r <= 0x1FFF:
		return true // Latin Extended Additional, Greek Extended
	case r >= 0x2010 && r <= 0x2027, r >= 0x2030 && r <= 0x205E:
		return r != 0x203C && r != 0x2049 // punctuation (— “ ” • …), not ‼ ⁉
	case r >= 0x2070 && r <= 0x20C0:
		return true // super- and subscripts, currency
	case r >= 0x2500 && r <= 0x259F:
		return true // box drawing, block elements
	case r >= 0x2800 && r <= 0x28FF:
		return true // braille (spinners)
	case r >= 0x2E80 && r <= 0x303E:
		return r != 0x3030 && r != 0x303D // CJK radicals, symbols, punctuation
	case r >= 0x3041 && r <= 0x31FF:
		return true // kana, bopomofo, compatibility jamo
	case r >= 0x3400 && r <= 0x4DBF, r >= 0x4E00 && r <= 0x9FFF, r >= 0xA000 && r <= 0xA4CF:
		return true // CJK ideographs, Yi
	case r >= 0xAC00 && r <= 0xD7A3:
		return true // Hangul syllables
	case r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE4F:
		return true // CJK compatibility
	case r >= 0xFF01 && r <= 0xFFEE:
		return true // halfwidth and fullwidth forms
	case r >= 0x20000 && r <= 0x3FFFD:
		return true // more CJK ideographs
	}
	return false
}

// dropRune reports whether r must not be sent to the terminal at all.
func dropRune(r rune) bool {
	switch {
	case r < 0x20, r >= 0x7F && r < 0xA0: // C0, DEL, C1 controls
		return true
	case r == 0x061C, r == 0x200B, r == 0x200E, r == 0x200F: // ALM, ZWSP, LRM, RLM
		return true
	case r >= 0x2028 && r <= 0x202E: // line/paragraph separators, bidi embeddings and overrides
		return true
	case r >= 0x2060 && r <= 0x206F: // word joiner, invisible operators, bidi isolates
		return true
	case r == 0xFEFF, r >= 0xFFF9 && r <= 0xFFFB: // BOM, interlinear annotations
		return true
	case r == utf8.RuneError:
		return false // shown as is
	}
	return false
}

// SafeContent returns the text to write for a cell of the given width:
// content without characters the terminal would act on rather than show,
// never starting with something the terminal would attach to the cell
// before (a combining or spacing mark, a joiner: given a blank to sit on
// instead), and never ending with a joiner that would pull the next cell in.
// A cell with nothing left to show becomes blanks.
func SafeContent(content string, width int) string {
	if plainASCII(content) {
		return content
	}
	clean := content
	if !utf8.ValidString(content) || strings.IndexFunc(content, func(r rune) bool { return dropRune(r) || r == 0xAD }) >= 0 {
		var b strings.Builder
		for _, r := range content { // invalid bytes decode as U+FFFD
			switch {
			case r == 0xAD:
				b.WriteByte('-') // soft hyphen: zero or one column, depending
			case !dropRune(r):
				b.WriteRune(r)
			}
		}
		clean = b.String()
	}
	clean = strings.TrimRight(clean, "\u200d\u200c")
	if clean == "" {
		return strings.Repeat(" ", max(width, 1))
	}
	if r, _ := utf8.DecodeRuneInString(clean); joinsBack(r, clean) {
		clean = " " + clean
	}
	return clean
}

// joinsBack reports whether text starting with r would be attached by the
// terminal to the cell before it.
func joinsBack(r rune, text string) bool {
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc, unicode.Cf) ||
		r >= 0x1160 && r <= 0x11FF { // Hangul vowels and finals
		return true
	}
	if r < 0x300 {
		return false
	}
	cl, _, _, _ := uniseg.FirstGraphemeClusterInString("a"+text, -1)
	return len(cl) > 1 // extends the "a": a spacing mark, say (Thai ำ)
}

// safeLinkPart returns a hyperlink's URL or parameters without control
// characters, which would end or alter the escape sequence carrying it.
func safeLinkPart(s string) string {
	if strings.IndexFunc(s, dropRune) < 0 && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if !dropRune(r) && r != utf8.RuneError {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// putUncertainCell writes a cell of uncertain width at the cursor, as
// described above. The pen is already set for it; content is the cell's
// text after SafeContent.
//
// The terminal may take the cell as anything up to two columns per code
// point (a cluster it splits, each part drawn wide), so every cell in that
// reach is written again after it, left to right. A neighbor of uncertain
// width extends the reach by its own. Each cell is written at most once
// more, so a line full of emoji costs a line's worth of cells, not more.
func (s *TerminalRenderer) putUncertainCell(newbuf *RenderBuffer, cell *Cell, content string) {
	x, y := s.cur.X, s.cur.Y
	width := newbuf.Width()
	w := max(cell.Width, 1)
	end := min(x+hostReach(content, w), width) // columns the terminal may have covered
	wrapOff := false
	guard := func(to int) { // no wrapping while anything may reach the line's end
		if to >= width && !wrapOff {
			_, _ = s.buf.WriteString(ansi.ResetModeAutoWrap)
			wrapOff = true
		}
	}
	if w > 1 {
		_, _ = s.buf.WriteString(ansi.EraseCharacter(w))
	}
	guard(x + hostReach(content, w))
	_, _ = s.buf.WriteString(content)

	for cx := x + w; cx < end; {
		next := newbuf.CellAt(cx, y)
		if next == nil || next.IsZero() {
			cx++
			continue
		}
		_, _ = s.buf.WriteString(ansi.CursorPosition(cx+1, y+1))
		s.updatePen(next)
		nw := max(next.Width, 1)
		text := SafeContent(next.Content, next.Width)
		if UncertainWidth(text) {
			if nw > 1 {
				_, _ = s.buf.WriteString(ansi.EraseCharacter(nw))
			}
			reach := cx + hostReach(text, nw)
			guard(reach)
			end = max(end, min(reach, width))
		}
		_, _ = s.buf.WriteString(text)
		cx += nw
	}
	if wrapOff {
		_, _ = s.buf.WriteString(ansi.SetModeAutoWrap)
	}

	// Put the cursor where the renderer believes it is.
	if nx := x + w; nx < width {
		_, _ = s.buf.WriteString(ansi.CursorPosition(nx+1, y+1))
		s.cur.X, s.atPhantom = nx, false
	} else {
		// The cell ends the line: leave the cursor on it, in the state the
		// renderer expects after writing the last column (the next move
		// starts with a carriage return).
		_, _ = s.buf.WriteString(ansi.CursorPosition(x+1, y+1))
		s.cur.X, s.atPhantom = width, true
	}
}

// hostReach is the most columns a terminal could give content (in a cell w
// wide): two per code point that might show.
func hostReach(content string, w int) int {
	n := 0
	for _, r := range content {
		if !unicode.Is(unicode.Mn, r) {
			n += 2
		}
	}
	return max(n, w)
}

// noShift turns off inserting and deleting characters to update a line
// (see transformLine).
const noShift = true

// invalidCell is what Invalidate makes the renderer believe the terminal
// shows: equal to no real cell, so every cell is written again.
var invalidCell = Cell{Content: "\x00", Width: 1}

// hostReset puts back the terminal state goat's drawing relies on, in case
// something else changed it: the ASCII character set (G0, shifted in), no
// scroll region, origin mode and insert mode off, autowrap on, plain style,
// no hyperlink.
const hostReset = "\x1b(B\x0f" + "\x1b[r" + "\x1b[?6l" + "\x1b[4l" + "\x1b[?7h" + "\x1b[0m" + "\x1b]8;;\x1b\\"

// Invalidate makes the next Render rewrite every cell of newbuf in place,
// after resetting the terminal state drawing relies on. Unlike Erase it
// doesn't clear the screen first, so nothing flickers: cells that were
// right are written over with themselves.
func (s *TerminalRenderer) Invalidate(newbuf *RenderBuffer) {
	_, _ = s.buf.WriteString(hostReset)
	s.cur.Style, s.cur.Link = Style{}, Link{}
	s.cur.X, s.cur.Y = -1, -1 // unknown: the next move is absolute
	s.atPhantom = false
	s.oldhash, s.newhash = nil, nil
	if s.curbuf == nil {
		s.clear = true
		return
	}
	for y := range s.curbuf.Height() {
		line := s.curbuf.Line(y)
		for x := range line {
			line[x] = invalidCell
		}
	}
	for y := range newbuf.Height() {
		newbuf.TouchLine(0, y, newbuf.Width())
	}
}
