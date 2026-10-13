# Launch scripts

A launch script tells goat which programs to open, each in its own window,
when it starts: your editor in one window, a log tail in another, a build
watcher in a third, each in the right folder, with the right title and colors.

```
goat -script ~/work/project.goat
```

(`--script` works too.)

- [Quick start](#quick-start)
- [Running a script](#running-a-script)
- [The file format](#the-file-format)
- [The keys: cd, title, run, option](#the-keys)
- [Options](#options)
- [What happens at startup](#what-happens-at-startup)
- [When a program ends](#when-a-program-ends)
- [Problems and messages](#problems-and-messages)
- [Examples](#examples)
- [Good to know](#good-to-know)

---

## Quick start

Save this as `~/dev.goat`:

```
# my dev workspace

[app]
cd: ~/src/myproject
title: editor
run: vim .
option: focus: on

[app]
cd: ~/src/myproject
title: tests
run: go test ./... ; exec $SHELL
option: theme: dark

[app]
cd: /var/log
title: syslog
run: tail -f syslog
option: wrap: off
```

Then:

```
goat -script ~/dev.goat
```

goat starts with its usual background shell and opens three windows on top
of it: *editor*, *tests* and *syslog*. Each runs in its folder, and the
editor has the focus.

---

## Running a script

```
goat -script FILE
```

`-script` combines with goat's other options, e.g.
`goat -config ~/alt.toml -script FILE`.

**Make a script runnable on its own.** Give it a `#!` first line, which goat
reads as a comment, and make it executable:

```
#!/usr/bin/env -S goat -script
[app]
run: htop
```

```
chmod +x ~/dev.goat
~/dev.goat              # the same as: goat -script ~/dev.goat
```

`env -S` passes the two words `goat -script` (the system adds the script's
path). If `goat` isn't on your `PATH`, give its full path instead:
`#!/home/me/bin/goat -script`.

---

## The file format

A script is a plain text file of **lines**:

| Line | Meaning |
|---|---|
| empty | ignored |
| starting with `#` | a comment, ignored (including a `#!` first line) |
| `[app]` | starts a new app: everything up to the next `[app]` describes one window |
| `key: value` | one setting of the current app: `cd`, `title`, `run` or `option` |

Details:

- **Spaces** around keys and values don't matter. Indent lines if you like.
- **Keys are case-insensitive:** `CD:`, `Title:` and `RUN:` all work. `[APP]`
  works too.
- **Windows line endings** (CRLF) are fine, so a script edited on Windows
  works as is.
- **Comments are whole lines only.** A `#` after a value is part of the
  value: `title: build  # main` makes the title `build  # main`. (In `run:`
  the shell treats `# ...` as a comment anyway, so it does no harm there.)
- **A key given twice** in one app: the last one counts. This doesn't apply
  to `option:`, which you can repeat as often as you like, one option per
  line.
- **Lines before the first `[app]`**, other than comments, are ignored with
  a warning.
- **Other sections** like `[settings]` are ignored, with everything up to the
  next `[app]`, with a warning.
- **Size limit:** 1 MB.

---

## The keys

### `cd:` (working folder)

```
cd: ~/src/myproject
cd: /var/log
cd: logs              # relative: to the script's own folder
```

The folder the program starts in.

- `~` (or `~/...`) is your home folder.
- A **relative path** is relative to **the folder the script is in**, not
  the folder you ran goat from. A script in `~/proj` with `cd: logs`
  starts in `~/proj/logs`, wherever you start goat.
- **Without `cd:`** the program starts in the folder goat was started in.
- **A folder that doesn't exist** (or can't be opened) is reported, and the
  program starts in the folder goat was started in.

### `title:` (window title)

```
title: build server
```

The name shown in the window's title bar and in its tab on the status bar,
as `N:title` (e.g. `2:build server`).

- **Without `title:`** the window is named after the program running in it,
  as usual. That name follows the program: `bash`, then `vim` while vim
  runs.
- **With `title:`** the name stays fixed, whatever runs in the window, even
  if the program sets its own title (`title_source = "terminal"`).
- Control characters are removed.

### `run:` (the program)

```
run: htop
run: tail -f /var/log/syslog
run: make watch
run: ./server --port 8080 --verbose
run: cd frontend && npm run dev
run: FOO=1 BAR="two words" ./tool
run: go test ./... ; exec $SHELL
```

The program to run in the window, with its arguments. The line is given to
**your shell**: the `shell` setting in `config.toml`, else `$SHELL`, else
`/bin/sh`, as `SHELL -c "the line"`. So everything you'd type at a prompt
works:

- quotes, `~`, `$VARIABLES`, `*.log`
- `&&`, `;` and `|`
- `VAR=value` before a command
- a relative program like `./server`, found in the `cd:` folder

The window shows the program's name (`htop`, `tail`, ...), unless `title:`
sets one.

**Without `run:`** the window gets an interactive shell, like Alt+c.

**When the program ends,** the window closes, or stays open if the program
failed; see [When a program ends](#when-a-program-ends). To keep working in
the window after a command finishes, end the line with `; exec $SHELL`:

```
run: make build ; exec $SHELL     # build, then a shell in the same window
```

### `option:` (one option)

```
option: wrap: off
option: theme: dark
```

The form is `option: NAME: VALUE`. `option: NAME = VALUE` works too. Give
**one option per line** and as many lines as you like; they're described in
the next section.

---

## Options

| Option | Values | Default |
|---|---|---|
| [`wrap`](#wrap) | `on` / `off` | `wrap_lines` from config.toml |
| [`theme`](#theme) | `dark` / `light` | (see [Colors](#colors)) |
| [`bg`](#bg-and-fg) | color name, `0`–`255`, `#rrggbb`, `default` | (see [Colors](#colors)) |
| [`fg`](#bg-and-fg) | color name, `0`–`255`, `#rrggbb`, `default` | (see [Colors](#colors)) |
| [`frame`](#frame) | `full` / `compact` / `none` | `frame` from config.toml |
| [`size`](#size) | `COLUMNSxROWS` | `new_window_width_ratio` / `_height_ratio` |
| [`pos`](#pos) | `X,Y` | a little below and right of the previous window |
| [`pin`](#pin) | `on` / `off` | `off` |
| [`focus`](#focus) | `on` / `off` | the last window gets the focus |
| [`hold`](#hold) | `failed` / `on` / `off` | `failed` |

**Writing values:**
- **On/off values** also accept `true` / `false`, `yes` / `no` and `1` / `0`.
- **Option names and values are case-insensitive.**
- **Alternative names** also work: `background` for `bg`, `foreground` or
  `text` for `fg`, `position` for `pos`.

### `wrap`

```
option: wrap: off
```

Whether long lines wrap at the window's edge, like Alt+z (`keys.toggle_wrap`).

- **`on`:** long lines continue on the next line, as in any terminal.
- **`off`:** long lines run past the right edge. Scroll sideways with
  Alt+< / Alt+> or Shift+mouse wheel. The window's bottom border shows
  `nowrap`, or `nowrap +40` when scrolled. This is good for logs and wide
  tables. Lines can be up to `nowrap_width` columns long (config.toml,
  default 512).

Alt+z still switches the window either way afterwards.

### `theme`

```
option: theme: dark
```

Gives the window a background of that tone (dark or light) from your
`.color` files (`dark-background.color`, `light-background.color`).

- **Each window** that asks for the same tone gets **the next one** of that
  tone, so several `theme: dark` windows look different: the first gets the
  first dark background, the second the second, and so on, wrapping around.
- **The text color** becomes one that reads well on that background: the
  terminal's own text color if it shows well enough, else the first
  suitable text color for that tone.
- **Without the `.color` files** (`goat -install` fetches them), `theme`
  is reported and has no effect.

> The first dark background in the standard files is *Obsidian* (pure black).
> On a terminal that is black already, the first `theme: dark` window looks
> uncolored. Use `bg:` to pick a particular color.

### `bg` and `fg`

```
option: bg: Midnight
option: fg: Gold
option: bg: 17
option: fg: #ffd700
option: bg: default
```

The window's background (`bg`) and text color (`fg`). A value is one of:

- **a name from your `.color` files** (case-insensitive): `bg` looks among
  the backgrounds (*Midnight*, *Ivory*, ...), `fg` among the text colors
  (*Gold*, *Ink*, ...);
- **a palette number** `0`–`255`;
- **`#rrggbb`**;
- **`default`**, the terminal's own color.

How they combine with other color settings:

- `bg` **wins over `theme`.**
- Giving only `fg` keeps the background the window would have had anyway
  (see [Colors](#colors)).
- **Text must stay readable.** If the `fg` you give wouldn't show on the
  window's background (below `min_text_contrast` in config.toml), goat uses
  a readable one instead and says so:
  `fg: Gold wouldn't show on that background; using Ink`.
- **A name or color goat doesn't know** is reported; that setting is then
  left out.

Alt+b and Alt+f work in the window afterwards as usual, going on from these
colors.

### `frame`

```
option: frame: none
```

The window's frame, for this window only (config.toml's `frame` sets it for
all others):

- **`full`:** a box with a title box at the top.
- **`compact`:** a box with the title in the top border.
- **`none`:** only a top line with the title. There are no side or bottom
  borders, so the content gets more room.

### `size`

```
option: size: 100x30
```

The window's size in columns × rows, **frame included**.

- **Without it:** the usual new-window size (`new_window_width_ratio` and
  `new_window_height_ratio` of the screen, by default half of each way).
- **Larger than the screen:** cut down to fit.
- **Smaller than the minimum:** raised to the minimum
  (`min_window_cols` / `min_window_rows`, more if the frame needs it).

### `pos`

```
option: pos: 0,0
option: pos: 40,2
```

The window's top-left corner: column `X`, row `Y`, counted from `0,0`, the
screen's top-left.

- **Without it:** each window opens a little below and to the right of the
  one before (`cascade_offset_x` / `_y`). The first one is centered.
- **A window that would stick out** past the right edge or the status bar
  is moved left or up until it fits.

`size` and `pos` together let you tile the screen. See
[the tiled example](#tiled-side-by-side).

### `pin`

```
option: pin: on
```

Keeps the window above all others, even when another window has the focus,
like Alt+w. It has a pinned border color (`pinned_border`). Alt+w unpins it.

### `focus`

```
option: focus: on
```

This window has the focus once all windows are open (and is brought to the
front), so you can start typing in it.

- **Without it:** the last window in the script has the focus.
- **Several windows with `focus: on`:** the last of them gets it.

### `hold`

```
option: hold: on
```

What happens to the window when its program ends. See
[When a program ends](#when-a-program-ends).

| Value | Window stays open... |
|---|---|
| `failed` (default) | only if the program failed (non-zero exit status, or killed by a signal) |
| `on` | always |
| `off` | never: the window closes, whatever happened |

Alternatives: `always` / `yes` for `on`, `never` / `no` for `off`, and
`error` / `on-error` for `failed`.

---

## What happens at startup

1. **Before anything appears,** goat reads the script. If the file can't be
   read (missing, a folder, no permission), goat says so and exits:
   ```
   goat: script /home/me/dev.goat: stat /home/me/dev.goat: no such file or directory
   ```
   **Mistakes inside the file** don't stop it; see
   [Problems and messages](#problems-and-messages).
2. **The background shell starts** as usual: the full-screen window 0, with
   its saved colors.
3. **The apps' windows open in the order they're listed,** numbered 1, 2,
   3, ... as they would be with Alt+c. Alt+1..9 jumps to them.
4. **The window given `focus: on`** (or else the last one) gets the focus.
5. **The status bar** says `dev.goat: opened 3 windows`, unless there are
   problems to report instead.

After that, everything works as usual: Alt+c opens more windows, Alt+x
closes them, Alt+s swaps one with the background, and so on.

### Colors

A window's colors are decided in this order:

1. **The colors any new window gets** (Alt+c). With
   `new_window_next_colors = true` (the default), that's the next
   background after the window before it. So each script window, like each
   Alt+c window, differs from the previous one. With `false`, it gets the
   previous window's colors.
2. **`theme`** replaces the background with the next one of that tone.
3. **`bg`** replaces the background with that color.
4. **`fg`** sets the text color.
5. **If the text wouldn't show** on the background, it's replaced with a
   readable one (`min_text_contrast`).

The background window keeps its own colors; a script doesn't change them.

---

## When a program ends

When the program in a window ends:

- **It succeeded** (exit status 0): the window closes, unless `hold: on`.
- **It failed:** the window **stays open** (`hold: failed`, the default) and
  shows what the program printed. At the end, it says how the program
  ended:

  ```
  /bin/sh: 1: ./no-such-tool: not found
   [broken exit status 127 - press Enter to close]
  ```

  That way a typo in `run:` or a missing tool doesn't make the window
  vanish before you can see why.

**While a window is kept open like this:**
- **Enter** closes it, and so does **Alt+x**.
- **Other keys** are ignored: the program is gone.
- **Scrolling back** (Alt+PageUp, mouse wheel), selecting and copying text,
  moving and resizing all still work.

**How the end is described:**

| Message | Meaning |
|---|---|
| `exited` | it ended normally (shown with `hold: on`) |
| `exit status N` | it ended with error code N (127 usually means "command not found", 126 "not executable") |
| `signal: killed`, `signal: terminated`, ... | it was stopped by a signal |

**Windows without `run:`** (a plain shell) close when you `exit` the shell,
however it exits, like Alt+c windows, unless `hold: on`.

---

## Problems and messages

goat reports mistakes in a script without refusing to start. Every app
that makes sense still opens.

| Problem | What happens |
|---|---|
| file can't be read | goat prints the reason and exits, before the screen is taken over |
| no `[app]` at all | reported: nothing to launch |
| unknown key (`colour: red`) | reported; the line is ignored |
| unknown option or bad value (`option: wrap: maybe`) | reported; that option is ignored, the app still opens |
| line before the first `[app]` | reported; ignored |
| unknown section (`[settings]`) | reported; ignored up to the next `[app]` |
| `cd:` folder missing | reported; the program starts in the folder goat was started in |
| `theme` without the `.color` files | reported; no effect |
| unknown color in `bg` / `fg` | reported; that setting is ignored |
| `fg` unreadable on the background | reported; a readable text color is used |
| program not found / fails | the window stays open showing the error ([`hold`](#hold)) |

Messages name the script and the line, e.g.

```
dev.goat:14: option "wrap: maybe": want on or off
dev.goat:20: cd: /no/such/folder isn't a folder goat can open; starting in /home/me
```

**Where they appear:**
- **The status bar** shows them: mistakes in the file as "config: ..."
  (the first, with "+N more" if there are others), then any found while
  opening the windows (such as a missing folder), each replacing the last.
- **The events window** (Alt+-) lists them all.
- **goat's log** has them too (`~/.local/state/goat/goat.log`).
- **Mistakes found while reading the file** are also printed to the terminal
  when goat exits.

---

## Examples

### A development workspace

```
#!/usr/bin/env -S goat -script
# Go service: editor, tests, server, logs

[app]
cd: ~/src/orders
title: edit
run: vim .
option: size: 120x40
option: pos: 0,0
option: focus: on

[app]
cd: ~/src/orders
title: server
run: go run ./cmd/server --port 8080
option: theme: dark
option: hold: on

[app]
cd: ~/src/orders
title: tests
run: gotestsum --watch ./...
option: theme: dark

[app]
cd: ~/src/orders/logs
title: logs
run: tail -F server.log
option: wrap: off
option: theme: light
```

The server window stays open even if the server stops cleanly
(`hold: on`), so its last messages can be read. The two `theme: dark`
windows get different dark backgrounds.

### Tiled: side by side

On a 160×48 terminal (the status bar takes the last row):

```
[app]
title: left
cd: ~/notes
run: vim todo.md
option: pos: 0,0
option: size: 80x47
option: frame: compact

[app]
title: right top
run: htop
option: pos: 80,0
option: size: 80x24
option: frame: compact

[app]
title: right bottom
cd: /var/log
run: tail -f syslog
option: pos: 80,24
option: size: 80x23
option: frame: compact
option: wrap: off
```

On a smaller terminal, windows are cut down to fit and moved inside the
screen, so the layout still works, just overlapping.

### Watching servers over ssh

```
# One window per server, each with its own light background

[app]
title: web-1
run: ssh -t web-1 'htop'
option: theme: light

[app]
title: web-2
run: ssh -t web-2 'htop'
option: theme: light

[app]
title: db
run: ssh -t db-1 'sudo tail -f /var/log/postgresql/postgresql.log'
option: bg: Lavender
option: wrap: off
```

If a server can't be reached, ssh fails and its window stays open showing
the ssh error. Press Enter to close it.

### Run a command, then keep a shell

```
[app]
cd: ~/src/app
title: build
run: make clean all ; exec $SHELL
```

After `make` finishes, its output stays on screen and you get a prompt in
the same window and folder. Without `; exec $SHELL`, the window would
close if the build succeeded, or stay open with a "press Enter" note if it
failed.

### Plain shells in different folders

```
[app]
cd: ~/src/frontend
title: frontend

[app]
cd: ~/src/backend
title: backend

[app]
cd: ~/deploy
title: deploy
option: pin: on
option: frame: none
option: size: 60x12
option: pos: 100,0
```

Three interactive shells, each in its folder. The small *deploy* window stays
on top in the corner.

### The example from `script.txt`

```
#!/aaa/bbb
# comment

[app]
cd: /ddd/fff
option: wrap: off
title: aaaaaaa
run: ./aaa -bbb -ccc

[app]
cd: /ddd/ggg
option: wrap: on
option: theme: dark
title: bbbbbbbb
run: ./bbb -aaa -ccc
```

This opens two windows:
- **aaaaaaa** runs `./aaa -bbb -ccc` in `/ddd/fff`, with long lines not
  wrapped.
- **bbbbbbbb** runs `./bbb -aaa -ccc` in `/ddd/ggg`, wrapped, on a dark
  background.

The order of the lines inside an `[app]` doesn't matter.

---

## Good to know

- **Only floating windows.** A script opens windows over the background
  shell; it doesn't change the background shell itself. To run a program
  *as* the background, open it from the script and press Alt+s in its
  window.
- **One option per line.** `option: wrap: off, theme: dark` is read as an
  unknown value. Use two `option:` lines.
- **No comments after values** (see [The file format](#the-file-format)).
- **Environment:** programs get the same environment as goat's shells
  (`TERM=xterm-256color`, ...). Per-program variables go in `run:`:
  `run: RUST_LOG=debug ./server`.
- **Interactive programs** (vim, htop, less, ssh) work normally. Each window
  is a full terminal of its own size and is told when it's resized.
- **Colors when goat starts:** the terminal reports its own colors only
  after goat has started. Until then, the readability check assumes a dark
  terminal.
- **The same script, different machines:** relative `cd:` paths and `~`
  make one script work wherever it's kept. Settings for one machine (like
  the host name) belong in `local.toml`, not in the script.
