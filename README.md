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
- Status bar: a numbered tab per window (click to focus), messages, a clock
- Per-window colors: Alt+b steps through background:text pairs you configure;
  the border takes the background too. Each new window starts at the next
  pair after the window it came from (`new_window_next_colors = false` to
  turn that off)
- Keyboard-driven window management, plus mouse: drag the title bar to move,
  drag the left/right/bottom edges or bottom corners to resize, click to focus
- Full rendering: 256-color and true color, bold/italic/underline/reverse,
  wide (CJK/emoji) characters
- Programs that use the mouse (vim, htop, mc, ...) get mouse clicks, drags and
  the wheel; in full-screen programs without mouse support the wheel scrolls
  (like xterm's alternate scroll)
- Per-window scrollback (mouse wheel or Alt+PageUp/PageDown)
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
| Next window colors        | `Alt+b` (from `window_colors`)  |
| Scroll history            | `Alt+PageUp` / `Alt+PageDown`   |
| Close window              | `Alt+x`                         |
| Quit goat                 | `Alt+q`                         |

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

goat reads `$XDG_CONFIG_HOME/goat/config.toml` or
`~/.config/goat/config.toml`, and falls back to float's
`~/.config/float/config.toml`, so an existing float config keeps working. See
[`config.example.toml`](config.example.toml) for every option.

Mistakes in the config never stop goat: unknown keys and invalid values are
shown in the status bar and listed again when goat exits, and defaults are
used.

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
