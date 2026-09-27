# Patched copy of ultraviolet

This is github.com/charmbracelet/ultraviolet at
v0.0.0-20260303162955-0b88c25f3fff (tests omitted), used by gloat through a
`replace` directive in gloat's go.mod.

## Patch: scroll by rotating lines (buffer.go)

`Buffer.InsertLineArea` / `Buffer.DeleteLineArea` shift lines by copying
every cell, one at a time. Terminal scrolling (a new line at the bottom of
the screen) goes through `DeleteLineArea`, so each scrolled line cost
width x height cell copies. In gloat that was 46% of CPU while printing
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

gloat's `uvpatch_test.go` checks the patched functions against the original
algorithm on random operations. Worth proposing upstream; once released
there, drop this directory and the `replace` line.
