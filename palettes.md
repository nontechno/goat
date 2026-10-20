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
╭───────────────┬───────────────┬───────palette─╮
│ Minty Fresh … │ Pea Soup …    │ Spice Rack …  │
│ …             │ …             │ …             │
│ …             ╔═══════════════╗ …             │
│ …             ║ Blue on Blue  ║ …             │
│ …             ╚═══════════════╝ …             │
│ …             │ …             │ …             │
╰ theme: dark · lingo: Pedantic ─ … Enter keep ─╯
```

It shows the theme's 36 palettes, top to bottom, column by column, each
named in the chosen style and drawn in its own colors. The chosen one has a
double frame in its own colors (covering the names just above and below
it). The bottom line shows the other two choices:

- **theme**: the kind of background. Dark, in-between (mid-tone) or light.
- **lingo**: the style of the names. Each palette has eight names, one per
  style: *Pedantic* ("Aquamarine on Black"), *Funny* ("Minty Fresh Void"),
  *Outrageous*, *Vulgar* (profane on purpose), *Corporate*, *Geeky*,
  *Wholesome* and *Sports*.

The window shows each palette as soon as you choose it, so you can see it
on your real text before deciding.

| Key | What it does |
|---|---|
| ↑ ↓ ← → | choose a palette (← → the column to the left / right) |
| Home / End | the first / last palette |
| t / Shift+t | the next / previous theme |
| l / Shift+l | the next / previous lingo |
| Delete / Backspace | no palette: the terminal's own colors |
| Enter | keep the choice and close |
| Esc (or Alt+t again) | put the window's colors back and close |
| click on a palette | choose it |
| click on the theme or lingo | the next one |
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

Each palette has its colors twice: as `#RRGGBB` and as xterm-256 color
indexes. `palette_colors` in config.toml says which goat uses:

- `"rgb"` (the default): the `#RRGGBB` colors. On terminals without 24-bit
  color (macOS Terminal, for one) goat shows the nearest of the 256 colors.
- `"ansi"`: the xterm-256 indexes, which look the same everywhere.
- `"auto"`: `#RRGGBB` when goat passes 24-bit color on (`true_color`),
  else the indexes.

The status window (Alt+F2) shows which is in use.

### New windows

A new window (Alt+c) gets the palette with **the next background** of the
same theme as the window it was opened from, with its text color in the
same place, so it stands out from that window. With
`new_window_next_colors = false` in config.toml it gets the same palette.

## What is remembered, and where

| Window | Where its palette is kept |
|---|---|
| the background window (Alt+0) | `local.toml`, `[background_window]` (`palette` = its name, plus `bg` / `fg`, its colors, for when the palettes file lacks it) |
| help, status, inventory, events | `system.toml`, `theme = <id>` in its section |
| other windows | nowhere: they end with their programs |

The lingo you chose is kept in `local.toml` as `palette_lingo`.

Launch scripts can set a window's palette too: `option: palette: Minty
Fresh Void` (a name in any style, or an id), or `option: theme: dark` for
the next one of a theme (see [scripting.md](scripting.md)).

## goat's own windows: system.toml

The help (Alt+F1), status (Alt+F2), inventory (Alt+F3) and events (Alt+-) windows can
be set up in `~/.config/goat/system.toml`, one section each:

```toml
# ~/.config/goat/system.toml

[help]
title = "❓Help"       # the title in its frame and its status bar tab
theme = 28649         # its palette: the palette's id
size = [40, 100%]     # width, height
position = [-40, 0]   # left, top

[status]
title = "Status"
size = [40, 100%]
position = [-80, 0]

[inventory]
size = [60%, 60%]
position = [-100, 0]

[events]
title = "Events"
size = [40, 100%]
position = [30, 0]
```

Every key is optional; a window without a section opens as it always did.
The keys that show the windows are fixed (Alt+F1, Alt+F2, Alt+F3, and
`show_events` in config.toml for the events window); an `activate` line
from earlier versions is ignored.

- **`title`**: shown in the window's frame and in its status bar tab.
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

goat checks the file every few seconds. Titles and palettes you
change by hand apply at once; a new size or position the next time the
window is shown. A file with a mistake (broken TOML) is reported once in the
events window, and the last good settings stay until it is fixed; a mistake
in one value (an unknown key, a size that isn't a number) leaves out just that
value.
