# Palettes

A window's colors in goat come from **palettes**: pairs of a background
color and a text color that look good together and stay readable. There
are 108 of them, from [nontechno/color](https://github.com/nontechno/color)
([usage](https://github.com/nontechno/color/blob/main/usage.md), [preview](https://color.nontechno.com/)).

They live in one file in goat's config folder:

```
~/.config/goat/terminal-color-combinations.json
```

`goat -install` downloads it, together with goat's config files, from
https://color.nontechno.com/terminal-color-combinations.json, or, if that
fails (or isn't a valid palettes file), from
https://raw.githubusercontent.com/nontechno/color/refs/heads/main/terminal-color-combinations.json.
As with the other files, nothing is written unless every download
succeeds, and a copy you changed is kept as `<name>.<time>.bak`. Without
it, windows keep the terminal's colors and Alt+t says to run
`goat -install`.

## Choosing a palette: Alt+t

Alt+t (`palette` in config.toml's `[keys]`) opens the palette dialog for
the active window:

```
╭─────────theme─┬───────────────────────────────────────palette─╮
│ dark          │ Minty Fresh … │ Pea Soup …    │ Spice Rack …  │
│ in-between    │ …             │ …             │ …             │
│ light         │ …             │ …             │ …             │
├─────────lingo─┤ …             │ …             │ …             │
│ Pedantic      │ …             │ …             │ …             │
│ Funny         │ …             ╔═══════════════╗ …             │
│ Outrageous    │ …             ║ Blue on Blue  ║ …             │
│ Vulgar        │ …             ╚═══════════════╝ …             │
│ Corporate     │ …             │ …             │ …             │
│ Geeky         │ …             │ …             │ …             │
│ Wholesome     │ …             │ …             │ …             │
│ Sports        │ …             │ …             │ …             │
╰───────────────┴─ Tab list · arrows choose · Enter keep · Esc ─╯
```

It has three lists, each with one item chosen:

- **theme**: the kind of background. Dark, in-between (mid-tone) or light.
- **lingo**: the style of the names. Each palette has eight names, one per
  style: *Pedantic* ("Aquamarine on Black"), *Funny* ("Minty Fresh Void"),
  *Outrageous*, *Vulgar* (profane on purpose), *Corporate*, *Geeky*,
  *Wholesome* and *Sports*.
- **palette**: the theme's 36 palettes, top to bottom, column by column.
  Each one is named in the chosen style and drawn in its own colors. The
  chosen one has a double frame (which covers the names just above and
  below it).

The window shows each palette as soon as you choose it, so you can see it
on your real text before deciding.

| Key | What it does |
|---|---|
| Tab / Shift+Tab | the next / previous list |
| ↑ ↓ | choose in the list |
| ← → | in the palette list: the column to the left / right (← from the first column goes to the lingo list) |
| Home / End | the first / last palette |
| Delete / Backspace | no palette: the terminal's own colors |
| Enter | keep the choice and close |
| Esc (or Alt+t again) | put the window's colors back and close |
| click | choose a theme, a lingo or a palette |
| double click on a palette | keep it and close |

While the dialog is up, programs keep running and their output keeps
showing; only your keys, clicks and pastes go to the dialog.

When you keep a palette, the status bar names it, with its theme and id:
`2:vim: Minty Fresh Void (dark, 29456)`. The id is how a palette is
written in files (below).

### What a palette changes

The window's background and text color, its frame (the border takes the
background; its lines and labels are made lighter or darker where they
wouldn't show), and the cursor color, where the terminal lets goat set
it. Programs that ask the terminal for its colors get the palette's.

On terminals with 24-bit color (`COLORTERM=truecolor`), goat uses each
palette's exact colors; elsewhere (macOS Terminal, for one) the nearest of
the 256 standard colors, which look the same everywhere.

### New windows

A new window (Alt+c) gets the palette with **the next background** of the
same theme as the window it was opened from, with its text color in the
same place, so it stands out from that window. With
`new_window_next_colors = false` in config.toml it gets the same palette.

## What is remembered, and where

| Window | Where its palette is kept |
|---|---|
| the background window (Alt+0) | `local.toml`, `[background_window]` (as its colors, with the palette's name in a comment) |
| help, status, inventory, events | `system.toml`, `theme = <id>` in its section |
| other windows | nowhere: they end with their programs |

The lingo you chose is kept in `local.toml` as `palette_lingo`.

Launch scripts can set a window's palette too: `option: palette: Minty
Fresh Void` (a name in any style, or an id), or `option: theme: dark` for
the next one of a theme (see [scripting.md](scripting.md)).

## goat's own windows: system.toml

The help (F1), status (F2), inventory (F3) and events (Alt+-) windows can
be set up in `~/.config/goat/system.toml`, one section each:

```toml
# ~/.config/goat/system.toml

[help]
title = "❓Help"       # the title in its frame and its status bar tab
activate = "f1"       # the key that shows and hides it
theme = 28649         # its palette: the palette's id
size = [40, 100%]     # width, height
position = [-40, 0]   # left, top

[status]
title = "Status"
activate = "f2"
size = [40, 100%]
position = [-80, 0]

[inventory]
activate = "f3"
size = [60%, 60%]
position = [-100, 0]

[events]
title = "Events"
activate = "alt-"
size = [40, 100%]
position = [30, 0]
```

Every key is optional; a window without a section opens as it always did.

- **`title`**: shown in the window's frame and in its status bar tab.
- **`activate`**: `"f1"` to `"f12"`, or Alt and a character: `"alt+h"`,
  `"alt-"` (Alt+-). It replaces the window's usual F-key. The events
  window also keeps Alt+- (`show_events` in config.toml). A key that is
  also one of goat's other shortcuts now shows this window instead.
- **`theme`**: a palette id (the `id` in the palettes file; the palette
  dialog shows it when you keep one). An id no palette has is reported in
  the events window.
- **`size`**: `[width, height]`, each a number of columns / rows or a
  percentage of the screen (`100%` fills it). Percentages may be written
  with or without quotes.
- **`position`**: `[left, top]` of the window's top-left corner. A
  negative number counts from the right / bottom edge: `-40` puts the left
  edge 40 columns from the right. It has no effect in a direction the
  window fills (`100%`). One number (`[30]`) sets only the left edge.
  `possition` is read too.

### goat writes it too

- **A palette chosen** for one of these windows (Alt+t while it is
  active) is written as its `theme`.
- **Moving or resizing** one of them writes its `size` and `position`,
  a couple of seconds after the last change, or as soon as you hide it. A
  direction it fills is written as `100%`; a position that was negative
  (counted from an edge) stays negative.
- **Only those keys** are rewritten, in their lines; a comment at the end
  of the line stays. Keys that aren't there are added to the window's
  section (made if missing). Everything else in the file stays as it is.
- The terminal changing size isn't written: only what you do.

goat checks the file every few seconds. Titles, keys and palettes you
change by hand apply at once; a new size or position the next time the
window is shown. A file with a mistake (broken TOML) is reported once in the
events window, and the last good settings stay until it is fixed; a mistake
in one value (an unknown key, `activate = "f99"`) leaves out just that
value.
