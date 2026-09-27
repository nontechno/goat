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
