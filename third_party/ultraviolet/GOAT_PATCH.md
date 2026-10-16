# Patched copy of ultraviolet

This is github.com/charmbracelet/ultraviolet at
v0.0.0-20260303162955-0b88c25f3fff (tests omitted), used by goat through a
`replace` directive in goat's go.mod.

## Patch: scroll by rotating lines (buffer.go)

`Buffer.InsertLineArea` / `Buffer.DeleteLineArea` shift lines by copying
every cell, one at a time. Terminal scrolling (a new line at the bottom of
the screen) goes through `DeleteLineArea`, so each scrolled line cost
width x height cell copies. In goat that was 46% of CPU while printing
large output, and it made big windows several times slower than small ones.

When the affected area spans whole lines (the usual case), the patch rotates
the line slices instead (one pointer move per line) and fills the new blank
lines directly. Partial-width areas keep the original code.

Effect on terminal-emulation throughput (x/vt, 10 MB of text):

| window  | before    | after     |
|---------|-----------|-----------|
| 98x12   | 4.9 MB/s  | 7.7 MB/s  |
| 98x46   | 2.4 MB/s  | 7.5 MB/s  |
| 200x46  | 1.7 MB/s  | 6.7 MB/s  |

## Patch: skip redundant damage checks (buffer.go, RenderBuffer.SetCell)

`RenderBuffer.SetCell` compared the old and new cell (a full style
comparison) on every write to decide whether to mark the line touched. The
comparison is now skipped when the line is already marked touched over those
columns, because marking it again would change nothing. x/vt never clears
the marks in goat's use, so this is nearly every character printed. The
result is identical.

## Patch: bulk fill (buffer.go, fillLine)

New blank lines are filled by setting one cell and copying doubling runs,
not by one struct copy per cell.

## Fix: areas beyond the buffer (buffer.go)

`InsertLineArea` / `DeleteLineArea` clip the area to the buffer first. An
area reaching past it, from a scroll region larger than the screen, used to
index out of range and panic. See also the x/vt patch notes.

goat's `uvpatch_test.go` checks the patched functions against the original
algorithm on random operations. Worth proposing upstream; once released
there, drop this directory and the `replace` line.

## Patch: characters of uncertain width (uncertain.go, terminal_renderer.go)

Terminals disagree about the width of emoji presentation sequences ("⚙️" is
two columns by Unicode, one in macOS Terminal), of symbols, arrows and
dingbats ("■ ✔ ↩", two columns where ambiguous characters are wide or drawn
as emoji), and of ZWJ sequences, flags and keycaps. The renderer trusted the
terminal's cursor after each character, so one of these shifted the rest of
the line, window borders included, out of place until those cells were
redrawn.

`putAttrCell` now writes such a cell with `putUncertainCell`: it blanks the
cell's columns first (ECH) when it has more than one, writes the character
(autowrap off at the end of a line), puts the cursor where it belongs (CUP),
and for a one-column cell redraws its right-hand neighbor, which a terminal
drawing the character two wide would have covered. The overwrite
optimization of cursor moves no longer prints such cells to move over them.
Other text is written exactly as before.

## Patch: nothing written can break the screen (uncertain.go, terminal_renderer.go)

Extends the patch above:

- Cell text goes through `SafeContent` first: no C0/C1 controls, bidi
  embeddings, overrides, isolates or marks, line/paragraph separators or
  other invisible format characters; a soft hyphen becomes "-"; a cell never
  starts with a mark or joiner (the terminal would attach it to the cell
  before; it gets a blank to sit on) or ends with a joiner (which would pull
  the next cell in). Hyperlink URLs and parameters lose control characters.
- `UncertainWidth` trusts only the scripts and blocks whose width no terminal
  disputes (Latin, Greek, Cyrillic, CJK, box drawing, ...); anything else,
  and any cluster extended by more than nonspacing marks, is written with
  care. The care now covers everything the terminal might have covered: up
  to two columns per code point (a cluster split into parts, each wide).
- A cell of width 0 that still has text takes a column like any other.
- `move` never stops on the right half of a wide character (upstream bug:
  the cells after it were written a column to the left).
- Lines are no longer updated by inserting or deleting characters (ICH,
  DCH, IRM): that shifts what the terminal shows, which next to a character
  of uncertain width isn't what goat thinks it is.
- `Invalidate` makes the next Render rewrite every cell in place (no clear,
  so no flicker), after putting back the character set, scroll region,
  origin, insert and autowrap modes, style and hyperlink. goat uses it to
  repaint (Alt+r, focus back, repaint_seconds).
