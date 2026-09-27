package main

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

type uvKey = uv.Key

func selWin(t *testing.T, text string) *Window {
	t.Helper()
	w := fakeWindow(1, 0, 0, 22, 6, false, false) // content 20x4 at (1,1)
	_, _ = w.emu.WriteString(strings.ReplaceAll(text, "\n", "\r\n"))
	return w
}

func TestSelectionCharMode(t *testing.T) {
	w := selWin(t, "hello world\nsecond line   \nthird")
	// Drag from 'w' of world (col 6, row 0) to 'n' of second (col 5, row 1).
	s := &selection{win: w, anchor: w.posAt(1+6, 1), head: w.posAt(1+5, 2), moved: true}
	if got := s.text(); got != "world\nsecond" {
		t.Fatalf("got %q", got)
	}
	// Reversed drag selects the same text.
	s.anchor, s.head = s.head, s.anchor
	if got := s.text(); got != "world\nsecond" {
		t.Fatalf("reversed: got %q", got)
	}
	// Trailing blanks are trimmed; a whole line past the text end.
	s = &selection{win: w, anchor: w.posAt(1, 2), head: w.posAt(1+19, 2), moved: true}
	if got := s.text(); got != "second line" {
		t.Fatalf("line: got %q", got)
	}
}

func TestSelectionClickWithoutMoveSelectsNothing(t *testing.T) {
	w := selWin(t, "hello")
	s := &selection{win: w, anchor: w.posAt(2, 1), head: w.posAt(2, 1)}
	if s.visible() || s.text() != "" {
		t.Fatal("a plain click must not select")
	}
}

func TestSelectionWordAndLine(t *testing.T) {
	w := selWin(t, "cd /usr/local/bin; l")
	s := &selection{win: w, anchor: w.posAt(1+8, 1), head: w.posAt(1+8, 1), mode: selWord}
	if got := s.text(); got != "/usr/local/bin" {
		t.Fatalf("word: got %q", got)
	}
	s.mode = selLine
	if got := s.text(); got != "cd /usr/local/bin; l" {
		t.Fatalf("line: got %q", got)
	}
}

func TestSelectionWideChars(t *testing.T) {
	w := selWin(t, "a日本b c")
	s := &selection{win: w, anchor: w.posAt(1, 1), head: w.posAt(1+5, 1), moved: true}
	if got := s.text(); got != "a日本b" {
		t.Fatalf("got %q", got)
	}
	s.mode, s.anchor, s.head = selWord, w.posAt(1+2, 1), w.posAt(1+2, 1)
	if got := s.text(); got != "a日本b" {
		t.Fatalf("word with wide chars: got %q", got)
	}
}

func TestSelectionFollowsScrolledText(t *testing.T) {
	w := selWin(t, "one\ntwo\nthree\nfour")
	s := &selection{win: w, anchor: w.posAt(1, 1), head: w.posAt(1+2, 1), moved: true}
	if got := s.text(); got != "one" {
		t.Fatalf("before: %q", got)
	}
	// More output scrolls "one" into history; the selection keeps it.
	_, _ = w.emu.WriteString("\r\nfive\r\nsix")
	if got := s.text(); got != "one" {
		t.Fatalf("after scrolling: %q", got)
	}
	// Selecting while viewing history also maps to the right lines.
	w.scroll = 2
	s = &selection{win: w, anchor: w.posAt(1, 1), head: w.posAt(1+4, 1), moved: true}
	if got := s.text(); got != "one" {
		t.Fatalf("scrolled view: %q", got)
	}
}

func TestSelectionHighlightAndClear(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 30, 8)
	w := selWin(t, "hello world")
	m.windows, m.focused = []*Window{w}, w
	var copied string
	m.copy = func(s, _ string) { copied = s }

	m.startSelection(w, 1, 1)
	m.extendSelection(1+4, 1)
	m.finishSelection()
	if copied != "hello" {
		t.Fatalf("copied %q", copied)
	}
	if !m.sel.contains(w, w.absLine(0), 2) || m.sel.contains(w, w.absLine(0), 6) {
		t.Fatal("highlight range wrong")
	}
	// Typing into the window clears the selection.
	_ = m.handleKey(keyText("x"))
	if m.sel != nil {
		t.Fatal("selection not cleared by typing")
	}
}

func TestDoubleClickSelectsWord(t *testing.T) {
	m := newWM(DefaultConfig(), nil, 30, 8)
	w := selWin(t, "hello world")
	m.windows, m.focused = []*Window{w}, w
	var copied string
	m.copy = func(s, _ string) { copied = s }
	for range 2 {
		m.startSelection(w, 1+7, 1)
		m.finishSelection()
	}
	if copied != "world" {
		t.Fatalf("double-click copied %q", copied)
	}
	m.startSelection(w, 1+7, 1) // third click: the whole line
	m.finishSelection()
	if copied != "hello world" {
		t.Fatalf("triple-click copied %q", copied)
	}
}

func keyText(s string) uvKey { return uvKey{Code: []rune(s)[0], Text: s} }
