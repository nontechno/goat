# Patched copy of x/vt

This is github.com/charmbracelet/x/vt at v0.0.0-20260927004216-9c77d672503d,
used by goat through a `replace` directive in goat's go.mod. Its own tests
are kept and pass. The `replace` in this directory's go.mod only matters for
running those tests (it points them at the patched ultraviolet); goat
ignores it. Changes are marked `GOAT PATCH` in the source.

## Speed: printing and scrollback

Profiling `cat` of a large file showed the emulator spending its time on
per-character overhead and on scrollback bookkeeping, not on real work.

- **scrollback.go:** the scrollback is a ring. It used to delete the first
  element of a slice once full, which moved all 1000+ line headers on every
  new line, and it allocated a fresh copy of every line (about 5 GB of garbage
  per 20 MB of output). Now a pushed line reuses the storage of the line it
  evicts whenever it fits. The trailing-blank scan uses a direct field check,
  exactly equivalent to `IsZero() || Equal(&EmptyCell)`, instead of a full
  style comparison per cell.
  One visible difference: a cell pointer or line from the scrollback is
  valid only until the next push. goat reads cells on the same goroutine
  that writes, so this is safe there.
- **utf8.go:** printing an ASCII character no longer allocates a one-byte
  string (it comes from a table). It also no longer looks up the auto-wrap
  mode in a map; `Emulator.autowrap` mirrors it and is kept by `setMode`,
  which every mode change and reset goes through. The printed cell is built
  from the cursor pen in place rather than through two struct copies.

Together with the ultraviolet patches (see its GOAT_PATCH.md):

| window | original vt + uv | patched |
|--------|------------------|---------|
| 98x23  | 7.5 MB/s         | ~21 MB/s |
| 200x46 | 7.0 MB/s         | ~18 MB/s |

(10 MB of text, emulator only.) In goat, 50 MB in a full-screen 200x49
window went from ~10 s and 11.5 s of CPU to ~4 s and 4.2 s of CPU. The
scrollback now holds on to up to about 1/3 more memory than it strictly
uses (+8 MB with 1000 lines of 200 columns). The worst case is unchanged:
every line full width.

## Fix: scroll margins beyond the screen (screen.go)

DECSTBM and DECSLRM accepted margins past the screen edge, such as
`\e[3;15r` on a 5-line screen. The next scroll, insert/delete line or reverse
index then indexed past the buffer and **panicked**, so any program could
crash the emulator and, with it, all of goat. The margins are now clamped
to the screen when they are stored. The handlers still accept the same
sequences as before, as upstream's tests expect.

## Additions (scrollback.go, emulator.go, screen.go)

- `Scrollback.Pushed` / `Emulator.ScrollbackPushed`: how many lines were
  ever added to the history. Once the history is full, its length stops
  changing while every new line drops the oldest one; goat numbers lines by
  this count so a view scrolled back, and a selection, stay on their text.
- `SetScrollbackSize(0)` turns the history off. It used to be ignored,
  keeping the default of 10000 lines.

## Verification

- vt's own test suite passes.
- goat's `vtpatch_test.go` covers the crash cases, the ring's behavior
  (wrap, shrink/grow, clear, trimming, copies), and auto-wrap tracking.
- A differential test compares full emulator state: every cell with style
  and link, the cursor, damage tracking and all scrollback lines. It uses 112
  runs over real text and random escape-sequence streams, with resizes,
  scrollback resizes and clears in between. It checked this copy against the
  original sources with only the margin fix applied, and the outputs were
  byte-identical.

Worth proposing upstream (the crash fix especially). Once released there,
drop this directory and the `replace` line.

## Evicted scrollback lines (for pasture)

**scrollback.go:** `Scrollback.SetEvictHandler(fn)` registers a function
that sees each line, with its number (as counted by `Pushed`), just before
it is dropped to make room (oldest first),
from `Push` on a full buffer and from `SetMaxLines` shrinking it. pasture
uses it to move history that no longer fits in memory to disk. Lines
removed by `Clear` (ED 3) are not passed on: that history is meant to go.
