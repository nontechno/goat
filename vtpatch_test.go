package main

import (
	"io"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// Tests for the patches in third_party/vt (see its GOAT_PATCH.md).

func newTestEmu(t *testing.T, w, h int) *vt.Emulator {
	t.Helper()
	e := vt.NewEmulator(w, h)
	go func() { _, _ = io.Copy(io.Discard, e) }()
	t.Cleanup(func() {
		// As Window.close: Emulator.Close races with a pending Read.
		if pw, ok := e.InputPipe().(*io.PipeWriter); ok {
			_ = pw.Close()
		}
	})
	return e
}

func screenRow(e *vt.Emulator, y int) string {
	var b strings.Builder
	for x := 0; x < e.Width(); x++ {
		if c := e.CellAt(x, y); c != nil && c.Content != "" {
			b.WriteString(c.Content)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// Scroll margins reaching past the screen crashed upstream vt (index out of
// range) on the next scroll: any program in any window could take goat down.
func TestOversizedMarginsDoNotPanic(t *testing.T) {
	for name, seq := range map[string]string{
		"SU":        "\x1b[3;15r\x1b[5S",
		"SD":        "\x1b[3;15r\x1b[3T",
		"IL":        "\x1b[3;15r\x1b[4;1H\x1b[3L",
		"DL":        "\x1b[3;15r\x1b[4;1H\x1b[3M",
		"RI":        "\x1b[3;15r\x1b[3;1H\x1bM",
		"LR margin": "\x1b[?69h\x1b[5;30s\x1b[T\x1b[2S\x1b[L\x1b[M",
	} {
		t.Run(name, func(t *testing.T) {
			e := newTestEmu(t, 21, 5)
			_, _ = e.Write([]byte(seq))
		})
	}
}

// The region is clamped to the screen, as xterm does: 3;15 on a 5-line
// screen is 3;5, so a line feed on the last line scrolls lines 3-5 only.
func TestOversizedRegionIsClamped(t *testing.T) {
	e := newTestEmu(t, 10, 5)
	_, _ = e.Write([]byte("1\r\n2\r\n3\r\n4\r\n5\x1b[3;15r\x1b[5;1H\n"))
	want := []string{"1", "2", "4", "5", ""}
	for y, w := range want {
		if got := screenRow(e, y); got != w {
			t.Errorf("row %d = %q, want %q", y, got, w)
		}
	}
}

func lineOf(s string) uv.Line {
	l := make(uv.Line, 0, len(s)+3)
	for _, r := range s {
		l = append(l, uv.Cell{Content: string(r), Width: 1})
	}
	return append(l, uv.EmptyCell, uv.Cell{}, uv.EmptyCell) // trailing blanks
}

func lineText(l uv.Line) string {
	var b strings.Builder
	for _, c := range l {
		b.WriteString(c.Content)
	}
	return b.String()
}

func sbTexts(sb *vt.Scrollback) []string {
	var out []string
	for i := range sb.Len() {
		out = append(out, lineText(sb.Line(i)))
	}
	return out
}

func equalStrings(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00") && len(a) == len(b)
}

// The scrollback is a ring that reuses evicted lines' storage; it must still
// behave like the plain slice it replaced.
func TestScrollbackRing(t *testing.T) {
	sb := vt.NewScrollback(4)
	words := []string{"a", "bb", "ccc", "dddd", "e", "ffffff", "g", "hh", "i"}
	for _, w := range words {
		in := lineOf(w)
		sb.Push(in)
		in[0].Content = "X" // the buffer keeps its own copy
	}
	if got, want := sbTexts(sb), []string{"ffffff", "g", "hh", "i"}; !equalStrings(got, want) {
		t.Fatalf("after wrapping: %q, want %q (trailing blanks trimmed)", got, want)
	}
	if c := sb.CellAt(1, 0); c == nil || c.Content != "f" {
		t.Errorf("CellAt(1,0) = %v", c)
	}
	if sb.CellAt(9, 0) != nil || sb.CellAt(0, 4) != nil || sb.Line(-1) != nil {
		t.Error("out of range access should give nil")
	}
	var fromLines []string
	for _, l := range sb.Lines() {
		fromLines = append(fromLines, lineText(l))
	}
	if !equalStrings(fromLines, sbTexts(sb)) {
		t.Errorf("Lines() = %q, want %q", fromLines, sbTexts(sb))
	}

	sb.Push(lineOf("j"))
	sb.SetMaxLines(2) // shrink while the ring is rotated
	if got, want := sbTexts(sb), []string{"i", "j"}; !equalStrings(got, want) {
		t.Fatalf("after shrink: %q, want %q", got, want)
	}
	sb.SetMaxLines(3) // grow
	sb.Push(lineOf("k"))
	sb.Push(lineOf("l"))
	if got, want := sbTexts(sb), []string{"j", "k", "l"}; !equalStrings(got, want) {
		t.Fatalf("after grow: %q, want %q", got, want)
	}

	sb.Push(uv.Line{uv.EmptyCell, {}}) // an all-blank line is kept, empty
	if l := sb.Line(sb.Len() - 1); l == nil || len(l) != 0 {
		t.Errorf("blank line = %#v, want empty non-nil", l)
	}
	// A cell with a style or link is not blank.
	styled := uv.Line{{Content: "x", Width: 1}, {Content: " ", Width: 1, Style: uv.Style{Attrs: uv.AttrReverse}}, uv.EmptyCell}
	sb.Push(styled)
	if n := len(sb.Line(sb.Len() - 1)); n != 2 {
		t.Errorf("styled trailing space trimmed: len %d, want 2", n)
	}

	sb.Clear()
	if sb.Len() != 0 {
		t.Fatal("Clear left lines")
	}
	sb.Push(lineOf("m"))
	if got := sbTexts(sb); !equalStrings(got, []string{"m"}) {
		t.Errorf("after clear: %q", got)
	}
}

// Auto-wrap is cached outside the mode map; it must follow ?7l / ?7h and a
// full reset.
func TestAutowrapFollowsModes(t *testing.T) {
	e := newTestEmu(t, 5, 3)
	_, _ = e.Write([]byte("\x1b[?7labcdefg"))
	if got := screenRow(e, 0); got != "abcdg" {
		t.Errorf("no wrap: row 0 = %q, want %q", got, "abcdg")
	}
	_, _ = e.Write([]byte("\x1b[?7h\x1b[2;1Habcdefg"))
	if r1, r2 := screenRow(e, 1), screenRow(e, 2); r1 != "abcde" || r2 != "fg" {
		t.Errorf("wrap: rows = %q %q", r1, r2)
	}
	_, _ = e.Write([]byte("\x1b[?7l\x1bcabcdefg")) // RIS turns wrapping back on
	if r0, r1 := screenRow(e, 0), screenRow(e, 1); r0 != "abcde" || r1 != "fg" {
		t.Errorf("after RIS: rows = %q %q", r0, r1)
	}
}
