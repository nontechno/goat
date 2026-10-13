# nontechno/goat

**Floating window terminal multiplexer, written in Go.**

goat is a Go port of [float](https://github.com/Henktorius/float): freely
positioned, overlapping terminal windows inside your terminal, driven by
keyboard and mouse. It uses [charmbracelet/x/vt](https://github.com/charmbracelet/x/tree/main/vt)
to emulate each window's terminal and
[charmbracelet/ultraviolet](https://github.com/charmbracelet/ultraviolet) to
decode input and draw to the host terminal.

```
~/src/project$ make test          ╭─ 1:vim ────────────────────╮
ok   ./...  0.8s                  │ package main               │
~/src/project$                    │ func main() {              │
         ╭─ 2:htop ─────────────╮ │                            │
         │ CPU [|||||    31%]   │ └────────────────────────────┘
         └──────────────────────┘
 0:bash  1:vim  2:htop                            copied 12 characters  14:05
```

goat starts with a full-screen shell in the folder you launched it from,
with a one-line status bar at the bottom. Floating windows open on top of it.

## Features

- A full-screen background shell (window 0) in the launch folder, always
  underneath; floating, overlapping windows on top; pin one to keep it above
  the others
- Status bar: a numbered tab per window (click to focus), messages, a clock,
  and optionally the active window's directory (`status_show_dir`)
- New windows (Alt+c) start in the active window's current directory
- `user@host` on each framed window's bottom border (`show_user`,
  `show_host`, both on by default). The host is `hostname` in
  `~/.config/goat/local.toml` (up to 32 characters) if you want a custom
  label, else the system's host name. Both follow the window: `su`/`sudo -i` shows the new
  user, and while ssh runs in the window, the user and host it logs in to
  (Linux)
- Per-window colors from the `.color` files in `~/.config/goat` (installed
  by `goat -install`): Alt+b steps the background through the dark ones, then
  the light ones, then the terminal's own; Alt+f steps the text color through
  those made for the background's tone (dark or light). The border takes the
  background, and its lines and text are made lighter or darker where the
  theme's border colors wouldn't show on it; the cursor takes the text color
  (where the terminal allows setting it). Each new window starts at the next background after the
  window it came from (`new_window_next_colors = false`: the same colors as
  that window). Each press always shows a different color: a color listed twice (or
  that looks like the one before, or like the terminal's own) is passed
  over. Text colors that wouldn't show on the background (below
  `min_text_contrast`) are skipped or replaced. The background window's
  colors are saved in `local.toml` (`[background_window]`) and come back
  the next time goat starts.
  Without the `.color` files windows keep the terminal's colors
- Keyboard-driven window management, plus mouse: drag the title bar to move,
  drag the left/right/bottom edges or bottom corners to resize, click to focus
- Full rendering: 256-color and true color, bold/italic/underline/reverse,
  wide (CJK/emoji) characters
- Programs that use the mouse (vim, htop, mc, ...) get mouse clicks, drags and
  the wheel; in full-screen programs without mouse support the wheel scrolls
  (like xterm's alternate scroll)
- Long lines wrap, or not (`wrap_lines`, Alt+z per window): unwrapped lines run
  past the edge and the view scrolls sideways; the bottom border shows which
- Per-window scrollback (mouse wheel or keys, see below), with a position
  bar on the right edge while scrolled back
- Alt+s swaps the active window's program with the background's: each keeps
  its history, colors, wrapping and title, and is resized to its new place;
  the focus stays on the floating window
- Events window (Alt+-): bells, desktop notifications (OSC 9, OSC 777,
  kitty's OSC 99), title changes (OSC 0/2, listed when the title differs
  from the last one; a run of `[ ! ] Action Required ...` titles is listed
  once as an `action` event, in bold, and a run of Braille spinner titles
  like `⠋ Thinking` once as a `spinner` event, until another title comes),
  clipboard writes, programs exiting, `user@host` changes,
  config warnings and goat's errors, with the time and the window each came
  from. It is a regular window (move, resize, pin, scroll, select and copy)
  without a program; it stays until Alt+- hides it, and Alt+- brings it back
  where it was. While it is hidden, the status bar shows `!N` for the N
  events that came in. With it focused, arrows, PageUp/PageDown and
  Home/End scroll it
- Programs in windows can set the clipboard too (OSC 52, e.g. from vim, tmux
  or neovim); goat passes it on. Programs can't read the clipboard.
- Select text with the mouse and it is copied to the clipboard: through the
  terminal (OSC 52, works over SSH) and through tmux / `clip.exe` / `pbcopy` /
  `wl-copy` / `xclip` / `xsel` when available
- Bracketed paste, focus reporting, cursor shape changes, program-set titles
- Three frame styles: `full` (title box), `compact` (title in the top border)
  and `none` (just a top line with the title)
- Configurable colors and key bindings via TOML

## Requirements

- Linux, macOS or a BSD with a real terminal emulator
  (window titles from the running process need Linux's `/proc`; elsewhere the
  shell name or the program-set title is shown)
- Go 1.24.2 or newer to build

## Build and run

```bash
cd goat
go build        # produces ./goat
./goat
```

or `go install github.com/nontechno/goat@latest` once it is published.

Flags: `-config <file>` to use a specific config file, `-version`,
`-cpuprofile <file>` to record a CPU profile (for diagnosing performance).

## Keyboard shortcuts

| Action                    | Default                         |
|---------------------------|---------------------------------|
| New window                | `Alt+c`                         |
| Focus next / previous     | `Alt+n` / `Alt+p`               |
| Focus window N            | `Alt+1`–`9` (number in title)   |
| Focus the background shell | `Alt+0`                        |
| Move window               | `Alt+h/j/k/l` or `Alt+arrows`   |
| Resize window             | `Alt+H/J/K/L` or `Alt+Shift+arrows` |
| Pin / unpin (stay on top) | `Alt+w`                         |
| Next background color     | `Alt+b`                         |
| Next text color           | `Alt+f`                         |
| Scroll history by a page  | `Alt+PageUp` / `Alt+PageDown`   |
| Scroll history by 3 lines | `Alt+Shift+PageUp` / `PageDown` |
| Oldest line / live view   | `Alt+Home` / `Alt+End`          |
| Wrap long lines or not    | `Alt+z`                         |
| View left / right (no wrap) | `Alt+<` / `Alt+>`, Shift+wheel |
| Events window (bells, notifications, ...) | `Alt+-`         |
| Swap with the background  | `Alt+s`                         |
| Close window              | `Alt+x`                         |
| Quit goat (asks first if windows are open) | `Alt+q`          |

If Alt doesn't reach your terminal, press `Esc` then the letter within
`alt_timeout_ms` (200 ms by default). Esc followed by anything that isn't a
shortcut, such as an arrow key in vim, goes straight to the program.

**macOS:** Terminal and iTerm2 make Option type characters (Option+c = `ç`)
unless *Use Option as Meta key* is on (Terminal: Settings → Profiles →
Keyboard; iTerm2: Profiles → Keys → Left Option key = Esc+). goat also
understands those characters as the shortcuts they stand for
(`mac_option_keys`, automatic on macOS; set it to `"on"` when goat runs over
SSH from a Mac). Option+n can't work that way, because macOS holds it back as
an accent key; use Meta, or `Esc` then `n`.

If shortcuts don't work, run `goat -keys`: it shows which config file is in
use, whether `mac_option_keys` is on, and for each key you press the bytes
the terminal sends and what goat makes of them.

**Quitting** with floating windows open asks first: a "Quit goat?" box
lists them. `y` (or Alt+q again, or Enter on *Quit*) quits; `n`, `Esc` or
Enter on *Cancel* (the default) goes back. ←/→/Tab switch buttons, and
clicking one presses it. While the box is up, nothing reaches the windows.
With only the background shell, Alt+q quits at once.

The background shell can't be moved, resized, pinned or closed with Alt+x.
When it exits, goat quits, like a terminal does, unless floating windows
are still open; then a fresh background shell is started.

## Mouse

- **Move**: drag the title bar
- **Resize**: drag the left, right or bottom edge, or a bottom corner; with
  `frame = "none"`, drag either end of the window's top line, or the
  bottom-right 2x2 cells of the window
- **Focus**: click anywhere in a window, or its tab in the status bar
- **Scroll**: mouse wheel (history, or the program's own scrolling)
- **Select and copy**: drag inside a window; double-click selects a word,
  triple-click a line. The text is copied when you release the button and
  stays highlighted until you click or type. In programs that use the mouse
  (vim, htop, ...), hold **Shift** while selecting.

## Configuration

`goat -install` sets up the config folder (`~/.config/goat`, or
`$XDG_CONFIG_HOME/goat`): it downloads `config.toml` and `local.toml` from
https://goat.nontechno.com/configs/ and the color files
(`dark-background.color`, `dark-foreground.color`, `light-background.color`,
`light-foreground.color`) from https://goat.nontechno.com/colors/, then exits. A file that
can't be downloaded from there (or isn't a valid file of its kind) comes
from the backup copy at
https://raw.githubusercontent.com/nontechno/goat/refs/heads/resources/
(same `colors/` and `configs/` folders) instead. Nothing is written unless every
download succeeds; a file you changed is kept as `<name>.<time>.bak`.
`GOAT_INSTALL_URL` points it at another server (used instead of both).

goat reads `$XDG_CONFIG_HOME/goat/config.toml` or
`~/.config/goat/config.toml`, and falls back to float's
`~/.config/float/config.toml`, so an existing float config keeps working. See
[`config.example.toml`](config.example.toml) for every option.

Mistakes in the config never stop goat: unknown keys and invalid values are
shown in the status bar and listed again when goat exits, and defaults are
used.

## Launch scripts

`goat -script FILE` (or `--script`) opens a window for each app listed in
FILE when goat starts. [scripting.md](scripting.md) has the full
description, with examples:

```
#!/usr/bin/env goat
# comments and empty lines are ignored

[app]
cd: ~/src/project/logs
title: logs
run: tail -f app.log
option: wrap: off
option: theme: dark

[app]
cd: ~/src/project
title: build
run: make watch
option: focus: on
```

Each `[app]` is one window:

- `cd:` its working directory (`~` is your home folder; a relative path is
  relative to the script's folder). Without it: the folder goat started in.
- `title:` the window's title, instead of the program's name.
- `run:` the program and its arguments, run by your shell (`$SHELL -c`), so
  quotes, `~`, `$VAR` and `./relative` paths work. Without it: a shell.
- `option: NAME: VALUE`, one per line:

| Option | Values | |
|---|---|---|
| `wrap` | `on` / `off` | long lines wrap, or run past the edge |
| `theme` | `dark` / `light` | a background of that tone (each window that asks gets the next one) |
| `bg`, `fg` | a name from the `.color` files, `0`-`255` or `#rrggbb` | background / text color |
| `frame` | `full` / `compact` / `none` | this window's frame |
| `size` | `COLSxROWS`, e.g. `100x30` | window size, frame included |
| `pos` | `X,Y`, e.g. `10,2` | top-left corner |
| `pin` | `on` / `off` | keep above other windows |
| `focus` | `on` | this window has the focus (otherwise the last one) |
| `hold` | `failed` (default) / `on` / `off` | keep the window open when the program ends: only if it failed, always, never. It then shows how the program ended; Enter closes it |

Text colors are checked as with Alt+f: one that wouldn't show on the
background is replaced (and reported). Problems in the script (an unknown
option, a missing folder, ...) are shown in the status bar and the events
window (Alt+-); the apps that make sense still start. A script that can't be
read stops goat before it starts.

## Log

Problems are logged to `~/.local/state/goat/goat.log` (or
`$XDG_STATE_HOME/goat/goat.log`; set `log_file` and `log_level` in the
config). A crash is recorded there with its stack trace and the state of
every window, and goat still gives you your terminal back and tells you
where the report is. A window whose terminal emulator fails on some output
is reset instead of taking goat down, and the log keeps that output, so the
problem can be reproduced.

## Differences from float

Fixed along the way:

- `alt_timeout_ms` works at the top level and, for float compatibility, in `[layout]`
- Terminal queries (cursor position, device attributes, cursor style ...) are
  answered by a full emulator, so no stray replies end up in the shell
- Esc followed by a non-shortcut key is no longer swallowed; Esc then an arrow
  no longer moves the window
- Every key is sent: Home/End/PageUp/PageDown/Insert/Delete, F1–F12,
  modifier combinations, and application cursor mode for vim/less/zsh
- Resizing from the left edge resizes the program's terminal too
- Shrinking the terminal redraws cleanly and keeps every window reachable
- Text attributes and wide characters are rendered correctly
- Alt+1..9 focus stable, numbered windows instead of stack positions
- The terminal is always restored, even if goat panics
- New windows can't underflow on tiny terminals; closed shells are reaped
- Event driven: no 16 ms polling, `/proc` is read twice a second, not per frame
- Stays responsive under huge output (`cat` of a 50 MB file): program output
  is processed in 8 ms slices between input handling and frames (~40-60/s),
  a short queue slows the program down instead of buffering megabytes, and
  the terminal emulator does far less work per character and per line
  (patched copies in `third_party/`). Measured: 50 MB in a full-screen
  200x49 window takes ~4 s using ~4 s of CPU (was ~10 s and 11.5 s), and keys
  respond in ~20 ms instead of ~1 s.
- A program can't crash goat: scroll margins beyond the screen no longer
  crash the emulator, and if the emulator ever does fail on some input,
  only that window is reset.

Borrowed from [tvxterm](https://github.com/blacknon/tvxterm): mouse passthrough
to programs that ask for it, wheel scrolling of history, focus in/out
reporting, program-set window titles, `TERM=xterm-256color` for child shells,
and killing the process on close.

## License

MIT, like float. See [LICENSE](LICENSE).
