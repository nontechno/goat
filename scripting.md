# Launch scripts

A launch script tells goat which programs to open, each in its own window,
when it starts: your editor in one window, a log tail in another, a build
watcher in a third, each in the right folder, with the right title and
colors.

```
goat -script ~/work/project.goat
```

(`--script` works too.)

- [Quick start](#quick-start)
- [Running a script](#running-a-script)
- [How a script works](#how-a-script-works)
- [The file format](#the-file-format)
- [Blocks](#blocks)
- [The keys: run, cd, title, option](#the-keys)
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
cd: ~/src/myproject

{
	title: editor
	option: focus: on
	run: vim .
}
{
	title: tests
	option: theme: dark
	run: go test ./... ; exec $SHELL
}
{
	cd: /var/log
	title: syslog
	option: wrap: off
	run: tail -f syslog
}
```

Then:

```
goat -script ~/dev.goat
```

goat starts with its usual background shell and opens three windows on top
of it: *editor* and *tests* in `~/src/myproject`, and *syslog* in
`/var/log`. The editor has the focus.

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
run: htop
```

```
chmod +x ~/dev.goat
~/dev.goat              # the same as: goat -script ~/dev.goat
```

`env -S` passes the two words `goat -script`, and the system adds the
script's path. If `goat` isn't on your `PATH`, give its full path instead:
`#!/home/me/bin/goat -script`.

---

## How a script works

> In the examples here, a `# ...` after a line explains it for you; it isn't
> part of the script. In a real script a comment must be on a line of its
> own, see [The file format](#the-file-format).

A script is read top to bottom. There are two kinds of lines:

- **Settings** (`cd:`, `title:`, `option:`) say how the following windows
  should be: their folder, title, colors, size, and so on. A setting holds
  from its line to the end of the block it is in.
- **`run:`** opens a window, running a program, with the settings in effect
  at that line.

So this opens two windows, both in `~/proj` with long lines not wrapped:

```
cd: ~/proj
option: wrap: off
run: make watch
run: tail -f build.log
```

A setting only affects `run:` lines **after** it:

```
run: htop              # in goat's own folder: no cd: yet
cd: /var/log
run: tail -f syslog    # in /var/log
```

**Blocks** (`{` … `}`) keep settings apart. What is set inside a block
applies only there (and in blocks inside it); after the `}` the earlier
settings are back. See [Blocks](#blocks).

---

## The file format

| Line | Meaning |
|---|---|
| empty | ignored |
| starting with `#` | a comment, ignored (including a `#!` first line) |
| `{` | starts a block |
| `}` | ends the innermost block |
| `run: COMMAND` | opens a window running COMMAND |
| `cd: FOLDER` | the folder for the following windows |
| `title: TITLE` | the title for the following windows |
| `option: NAME: VALUE` | one option for the following windows |

Details:

- **Spaces and tabs** at the start and end of lines don't matter. Indent
  blocks as you like.
- **Keys are case-insensitive:** `CD:`, `Title:` and `RUN:` all work.
- **Windows line endings** (CRLF) are fine, so a script edited on Windows
  works as is.
- **`{` and `}` must each be alone on their line.** A brace inside a value
  is just a character: `run: echo {a,b}` is a command, not a block.
- **Comments are whole lines only.** A `#` after a value is part of the
  value: `title: build  # main` makes the title `build  # main`. (In `run:`
  the shell treats `# ...` as a comment anyway.)
- **Quotes:** a value in quotes, `"..."` or `'...'`, has the quotes
  removed, and keeps everything inside: spaces at its ends, braces, `#`.
  - `title: "  build  "` gives a title with its spaces.
  - `run: "./tool -x {{{ 1"` runs `./tool -x {{{ 1`.
  - Quotes are only removed when they are the single pair around the whole
    value. `run: "my tool" --name "x y"` is passed to the shell unchanged.
- **Setting the same thing twice** in one block: the later one counts for
  the `run:` lines after it.
- **Size limit:** 1 MB.

---

## Blocks

A block is a group of lines between `{` and `}`:

```
cd: ~/src
option: wrap: off
{
	cd: api
	title: api server
	option: theme: dark
	run: go run ./cmd/server
}
run: htop              # in ~/src, no title, no theme, not wrapping
```

**Inside a block:**
- **Settings from outside** carry in: the block above starts with
  `cd: ~/src` and `wrap: off`.
- **Settings made inside** replace them, for the rest of the block only:
  `cd: api`, `title: api server`, `theme: dark`.

**After the `}`,** the settings are as they were before the `{`, so the
`htop` window is in `~/src`, with no title and no theme.

**Blocks can be nested** to any depth. An inner block starts with the
settings in effect where it begins, including those of the blocks around
it.

**A block may open several windows**, and they all share its settings
(unless changed between them):

```
{
	cd: ~/logs
	option: wrap: off
	run: tail -f app.log
	run: tail -f db.log
}
```

**Blocks are optional.** A script without any is fine: settings then hold
to the end of the file.

### Folders in blocks: where relative `cd:` starts

A relative `cd:` (not starting with `/` or `~`) is taken from **the folder
the block started in**, not from an earlier `cd:` in the same block:

```
cd: /111
{
	cd: ./222        # /111/222   (from /111, where this block started)
	run: ...         #   runs in /111/222
	{
		cd: ../333   # /111/333   (from /111/222, where this block started)
		run: ...     #   runs in /111/333
	}
	cd: ../333       # /333       (from /111 again: the block started there)
	run: ...         #   runs in /333
}
run: ...             # /111       (the block's cd:s are gone)
```

**So, in a block:**
- `cd:` *chooses* the block's folder relative to where the block began; two
  `cd:`s in a row don't add up.
- **To go deeper step by step,** open a block for each step.

**Outside any block,** a relative `cd:` is taken from **the script's own
folder**. A script in `~/proj` with `cd: logs` uses `~/proj/logs`, wherever
you start goat from. That makes a script and the folders next to it
portable.

**Where no `cd:` is in effect,** windows start in the folder goat was
started in.

**Other `cd:` forms:**
- `~` and `~/...` are your home folder.
- An absolute path (`/var/log`) is used as is.
- `.` and `..` work as usual: `cd: .` is the folder the block started in.

---

## The keys

### `run:` (open a window)

```
run: htop
run: tail -f /var/log/syslog
run: ./server --port 8080 --verbose
run: cd frontend && npm run dev
run: FOO=1 BAR="two words" ./tool
run: go test ./... ; exec $SHELL
run:
```

Opens a window and runs the command in it, using the settings in effect at
this line. The command is given to **your shell** (config.toml's `shell`,
else `$SHELL`, else `/bin/sh`) as `SHELL -c "COMMAND"`, so everything you'd
type at a prompt works:

- quotes, `~`, `$VARIABLES`, `*.log`
- `&&`, `;` and `|`
- `VAR=value` before a command
- a relative program like `./server`, found in the window's folder

**`run:` with nothing after it** opens a window with an interactive shell,
like Alt+c.

**The window is named** after the program (`htop`, `tail`, ...), unless a
`title:` is in effect.

**When the program ends,** the window closes, or stays open if the program
failed; see [When a program ends](#when-a-program-ends). To keep working in
the window after a command finishes, end it with `; exec $SHELL`:

```
run: make build ; exec $SHELL     # build, then a shell in the same window
```

### `cd:` (folder)

```
cd: ~/src/myproject
cd: /var/log
cd: logs
cd: ../shared
```

The folder the following windows start in. See
[Folders in blocks](#folders-in-blocks-where-relative-cd-starts) for how
relative paths are resolved.

**A folder that doesn't exist** (or can't be opened) is reported when the
window opens, and the program starts in the folder goat was started in.

### `title:` (window title)

```
title: build server
title: "  padded  "
```

The name shown in the window's title bar and in its tab on the status bar,
as `N:title` (e.g. `2:build server`).

- **Without `title:`** the window is named after the program running in it,
  as usual. That name follows the program: `bash`, then `vim` while vim
  runs.
- **With `title:`** the name stays fixed, whatever runs in the window, even
  if the program sets its own title (`title_source = "terminal"`).
- **Several windows** opened in the same block share the title. Give each
  its own block, or a new `title:` before each `run:`.
- Control characters are removed. Use quotes to keep spaces at the ends.

### `option:` (one option)

```
option: wrap: off
option: theme: dark
```

The form is `option: NAME: VALUE`. `option: NAME = VALUE` works too. Give
**one option per line**; the options are described in the next section.

---

## Options

Each option holds, like any setting, until the end of its block.

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

Whether long lines wrap at the window's edge, like Alt+z
(`keys.toggle_wrap`).

- **`on`:** long lines continue on the next line, as in any terminal.
- **`off`:** long lines run past the right edge. Scroll sideways with
  Alt+< / Alt+> or Shift+mouse wheel. The window's bottom border shows `>`
  (or `> +40` when scrolled; `w` when wrapping). This is good for logs and wide
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

The window's frame (config.toml's `frame` sets it for the others):

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
- **Several windows** under the same `pos` open on top of each other; give
  each its own `pos` (or block).

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

The window has the focus once all windows are open (and is brought to the
front), so you can start typing in it.

- **Without it:** the last window opened has the focus.
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
3. **A window opens for each `run:`,** in the order they appear in the
   file, numbered 1, 2, 3, ... as they would be with Alt+c. Alt+1..9 jumps
   to them.
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

**Windows from an empty `run:`** (a plain shell) close when you `exit` the
shell, however it exits, like Alt+c windows, unless `hold: on`.

---

## Problems and messages

goat reports mistakes in a script without refusing to start. Every window
that makes sense still opens.

| Problem | What happens |
|---|---|
| file can't be read | goat prints the reason and exits, before the screen is taken over |
| no `run:` at all | reported: nothing to launch |
| unknown key (`colour: red`) | reported; the line is ignored |
| a line that isn't `key: value`, `{` or `}` | reported; ignored |
| unknown option or bad value (`option: wrap: maybe`) | reported; that option is ignored |
| `}` without a `{` | reported; ignored |
| `{` without a `}` | reported; the block ends at the end of the file |
| `cd:` folder missing | reported when the window opens; it starts in the folder goat was started in |
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

> **Scripts from earlier goat versions** used `[app]` sections. Those lines
> are now reported as not understood. To convert, replace each `[app]` with
> `{`, put a `}` after its lines, and move its `run:` line to the end of the
> block (a `run:` opens the window with the settings *above* it). An `[app]`
> that had no `run:` becomes an empty `run:`.

---

## Examples

### A development workspace

```
#!/usr/bin/env -S goat -script
# Go service: editor, tests, server, logs
cd: ~/src/orders

{
	title: edit
	option: size: 120x40
	option: pos: 0,0
	option: focus: on
	run: vim .
}

# dark backgrounds for the rest of the file
option: theme: dark
{
	title: server
	option: hold: on
	run: go run ./cmd/server --port 8080
}
{
	title: tests
	run: gotestsum --watch ./...
}
{
	cd: logs
	title: logs
	option: wrap: off
	option: theme: light
	run: tail -F server.log
}
```

**What this opens:**
- **All four windows** start in `~/src/orders`; *logs* is in its `logs`
  folder.
- **The server window** stays open even if the server stops cleanly
  (`hold: on`), so its last messages can be read.
- ***server* and *tests*** get different dark backgrounds, and *logs* a
  light one.

### One folder, several windows

```
cd: ~/src/app
option: wrap: off
title: logs
run: tail -f logs/app.log
run: tail -f logs/db.log
title: shell
run:
```

Three windows in `~/src/app`: two tails titled *logs*, then a shell titled
*shell*. No blocks needed when settings only add up.

### Nested folders

```
cd: ~/src/shop
{
	cd: backend
	# ~/src/shop/backend
	run: go run .
	{
		cd: migrations
		# ~/src/shop/backend/migrations
		run: ./migrate status ; exec $SHELL
	}
	cd: frontend
	# ~/src/shop/frontend (from ~/src/shop, where this block began)
	run: npm run dev
}
# a shell in ~/src/shop
run:
```

**Why `cd: frontend` and not `cd: ../frontend`:** the second `cd:` in the
block is taken from where the block began (`~/src/shop`), not from
`backend`. `../frontend` would be `~/src/frontend`.

### Tiled: side by side

On a 160×48 terminal (the status bar takes the last row):

```
option: frame: compact
{
	title: left
	cd: ~/notes
	option: pos: 0,0
	option: size: 80x47
	run: vim todo.md
}
{
	title: right top
	option: pos: 80,0
	option: size: 80x24
	run: htop
}
{
	title: right bottom
	cd: /var/log
	option: pos: 80,24
	option: size: 80x23
	option: wrap: off
	run: tail -f syslog
}
```

On a smaller terminal, windows are cut down to fit and moved inside the
screen, so the layout still works, just overlapping.

### Watching servers over ssh

```
# One window per server
option: theme: light

title: web-1
run: ssh -t web-1 'htop'
title: web-2
run: ssh -t web-2 'htop'
{
	title: db
	option: bg: Lavender
	option: wrap: off
	run: ssh -t db-1 'sudo tail -f /var/log/postgresql/postgresql.log'
}
```

**What this opens:** *web-1* and *web-2* get the first two light
backgrounds, and *db* is Lavender. If a server can't be reached, ssh fails
and its window stays open showing the ssh error; press Enter to close it.

### Quotes and braces

```
title: "  build  "
run: "./gen --template {{name}}.txt"
run: echo "{ not a block }"
```

**What this does:**
- **The title** keeps its spaces.
- **The first command** is run as `./gen --template {{name}}.txt`.
- **The second line** is a command too: its `{` isn't alone on the line, so
  it doesn't start a block.

### The example from `script2.txt`

```
#!/aaa/bbb
# comment

option: wrap: off
cd: /111

{
	cd: ./222
	# dir should be /111/222
	option: wrap: off
	run: ./aaa -bbb -ccc 111
	run: "./aaa -bbb -ddd {{{ 222"
	{
		# comment
		cd: ../333
		# dir should be /111/333
		title: "bbbbbbbb "
		option: theme: dark
		run: ./aaa -bbb -ccc 333
	}
	cd: ../333
	# dir should be /333
	option: theme: dark
	run: ./aaa -bbb -ccc 444
}

# dir should be /111
run: ./aaa -bbb -ccc 555
```

This opens five windows, none wrapping long lines:

| # | Runs | In | Title | Theme |
|---|---|---|---|---|
| 1 | `./aaa -bbb -ccc 111` | `/111/222` | (program's name) | |
| 2 | `./aaa -bbb -ddd {{{ 222` | `/111/222` | (program's name) | |
| 3 | `./aaa -bbb -ccc 333` | `/111/333` | `bbbbbbbb ` | dark |
| 4 | `./aaa -bbb -ccc 444` | `/333` | (program's name) | dark |
| 5 | `./aaa -bbb -ccc 555` | `/111` | (program's name) | |

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
- **The same script, different machines:** relative `cd:` paths (from the
  script's folder) and `~` make one script work wherever it's kept.
  Settings for one machine (like the host name) belong in `local.toml`, not
  in the script.
