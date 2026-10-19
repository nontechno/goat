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
- Aliases (`~/.config/goat/alias.toml`): shorter or friendlier names shown
  for host names, directories, git repositories and branches, e.g.
  `"/home/opc" = "🏠"` or `"*/main" = "🏡"`; see [aliasing.md](aliasing.md)
- Status bar: a numbered tab per window (click to focus), messages, a clock,
  the name of the host goat runs on (`💻myhost`), and optionally the
  active window's directory (`status_show_dir`). The clock and the host
  name can be turned off in `local.toml` with `show_clock = false` and
  `show_launch_host = false` (both on by default; edits show within
  seconds)
- New windows (Alt+c) start in the active window's current directory
- `🧑user 💻host` on each framed window's bottom border (`show_user`,
  `show_host`, both on by default). The host is the system's host name.
  Both follow the window: `su`/`sudo -i` shows the new
  user, and while ssh runs in the window, the user and host it logs in to
  (Linux)
- After them, when the window's current directory is in a git repository,
  the repository and branch: `└─ 🧑alice 💻myhost 📖nontechno/goat 🌿main ──[]─┘`
  (`show_git`, on by default). The owner and name come from the `origin`
  remote's URL (GitLab subgroups included: `group/sub/project`); a remote
  on a local path, or none, gives just a name (the remote's or the
  folder's). With no branch checked out it shows
  `🌿detached a1b2c3d`. goat reads `.git` itself (git needn't be
  installed): every window is followed, a branch switch shows within half a
  second and is listed in the events window. Not shown while the window
  runs ssh (the directory goat sees is then the local one), nor for the
  background shell, which has no frame
- Per-window colors from **palettes**: 108 background + text pairs from
  [nontechno/color](https://github.com/nontechno/color)
  (`terminal-color-combinations.json`, fetched by `goat -install`). Alt+t
  opens the palette dialog: choose a theme (dark, in-between, light), a
  style for the names (Pedantic, Funny, Outrageous, Vulgar, Corporate,
  Geeky, Wholesome, Sports) and one of the theme's 36 palettes, each shown
  in its own colors. The window shows each one as you move; Enter keeps
  it, Esc puts the old colors back, Delete goes back to the terminal's own.
  The border takes the background, and its lines and text are made lighter
  or darker where the theme's border colors wouldn't show on it; the cursor
  takes the text color (where the terminal allows setting it). Each new
  window gets the next background of the palette of the window it came
  from (`new_window_next_colors = false`: the same palette). The background
  window's palette is saved in `local.toml` (`[background_window]`), the
  palettes of goat's own windows in `system.toml`; both come back the next
  time goat starts. See [palettes.md](palettes.md)
- Keyboard-driven window management, plus mouse: drag the title bar to move,
  drag the left/right/bottom edges or bottom corners to resize, click to focus
- 24-bit color ("true color") is passed through exactly when the terminal
  has it, else rounded to the nearest of the 256 colors. goat works out
  which from `COLORTERM`, `TERM` and terminfo, and recognizes iTerm2 even
  over ssh (`LC_TERMINAL`, which ssh passes on where it drops `COLORTERM`),
  as well as WezTerm, ghostty and VS Code. `true_color = "on"` in config.toml
  forces it on; F2 (status) shows what goat decided and why. Programs in
  windows get `COLORTERM=truecolor` only when it is on, so they pick the
  same colors as they would outside goat
- Full rendering: 256-color and true color, bold/italic/underline/reverse,
  wide (CJK/emoji) characters
- What programs print stays inside their windows, whatever it is:
  - Characters terminals disagree on the width of (emoji like `⚙️`, symbols
    like `✔ ■ ↩`, flags, Indic conjuncts, anything outside the scripts all
    terminals agree on) are written with the cursor put in place after
    them, and the cells they might have covered written again. At worst
    such a character is cut off by its neighbor; borders never move.
  - Characters a terminal would act on instead of showing (bidi overrides
    and marks, C1 controls, line separators, other invisible format
    characters) never reach it, nor control characters in hyperlinks.
    Terminals that reorder right-to-left text by themselves (GNOME Terminal
    and other VTE ones) are asked not to while goat runs.
  - Anything else that garbles the screen (another program writing to the
    terminal, a reset, a stray character set switch) is repaired by a
    repaint: Alt+r, automatically when the terminal gets focus back, and
    every `repaint_seconds` if set. A repaint rewrites every cell in place
    (no flicker) after putting back the terminal state goat relies on.
- Programs that use the mouse (vim, htop, mc, ...) get mouse clicks, drags and
  the wheel; in full-screen programs without mouse support the wheel scrolls
  (like xterm's alternate scroll)
- Long lines wrap, or not (`wrap_lines`, Alt+z per window): unwrapped lines run
  past the edge and the view scrolls sideways; the bottom border shows which:
  `[]` wrapping, `[)` not. A new window (Alt+c) starts as the window it
  was opened from; the background shell and launch script windows start as
  `wrap_lines` says (a `wrap_lines` in `local.toml` overrides
  `config.toml`'s on that machine)
- Per-window scrollback (mouse wheel or keys, see below), with a position
  bar on the right edge while scrolled back
- Alt+0 focuses the background shell; pressed again, it hides every
  floating window (they keep running), and once more it brings back the
  ones still hidden. A hidden window's status bar tab shows a ghost
  (`👻2:vim`); clicking that tab or pressing its Alt+N brings back that
  window alone. Alt+n / Alt+p skip hidden windows. The events window isn't
  affected (only Alt+- shows and hides it)
- Alt+s swaps the active window's program with the background's: each keeps
  its history, colors, wrapping and title, and is resized to its new place;
  the focus stays on the floating window
- Events window (Alt+-): bells, desktop notifications (OSC 9, OSC 777,
  kitty's OSC 99), title changes (OSC 0/2, listed when the title differs
  from the last one; a run of `[ ! ] Action Required ...` titles is listed
  once as an `action` event, in bold, and a run of Braille spinner titles
  like `⠋ Thinking` once as a `spinner` event, until another title comes),
  clipboard writes, programs exiting, `user@host` changes, program status
  reports (see below), config warnings and goat's errors, with the time and
  the window each came
  from. It is a regular window (move, resize, pin, scroll, select and copy)
  without a program; it stays until Alt+- hides it, and Alt+- brings it back
  where it was. While it is hidden, the status bar shows `!N` for the N
  events that came in. With it focused, arrows, PageUp/PageDown and
  Home/End scroll it
- Program status (OSC 7501, the [Program Status Protocol](https://www.superlogical.com/rex/docs/build/program-status)):
  programs that report what they are doing (idle, working with progress,
  blocked on you for a permission, question or login, done, error) are
  understood. Every report is listed in the events window as a `status`
  event with all it carries (id, app, kind, progress, title, message;
  blocked and error in bold), and so are reports ignored for breaking the
  protocol's rules, with the reason. goat keeps each window's records; a
  new shell prompt (OSC 133;A) ends working and blocked ones, a full reset
  (`ESC c`) clears them all. Programs asking whether the protocol is
  supported (`ESC ] 7501 ; ? ST`) get `ESC ] 7501 ; ? ST`. Nothing else is
  shown for them yet; what goat does with each kind of report is in the
  (empty) `onStatus*` functions in `status.go`
- Help window (F1): every keyboard shortcut and mouse action, with the keys
  as `config.toml` binds them. A regular window without a program, like the
  events window: move it, resize it (the text is laid out again for the new
  width), scroll it, copy from it, and keep working in other windows
  meanwhile. F1 again hides it, whether or not it has the focus (Alt+x too)
- Status window (F2): goat itself. Version and build, since when and where
  it runs, its files (config, local, aliases, log); and, refreshed every
  second, the windows (floating, hidden, pinned, goat's own), the processes
  in them with their memory and CPU, goat's own memory, CPU and goroutines,
  history kept, events, and what programs report (OSC 7501). F2 again
  hides it
- Inventory window (F3): every window: its processes (everything under its
  shell, with memory and CPU), directory, user@host and git, size, history,
  hidden/pinned/ended, and its program's status reports (OSC 7501).
  Refreshed every second. ↑ ↓ (or a click) choose a window, Enter (or a
  double click) goes to it, bringing it back if Alt+0 hid it. F3 again
  hides it. Process figures come from `/proc` on Linux and `ps` on macOS,
  sampled every 2 s, only while the status or inventory window is open
- In both, CPU, memory and a program's reported progress are drawn as
  bars beside their numbers (`████▋      43%`): CPU as a share of all the
  machine's CPUs, memory of all its memory. The bars fill in eighths of a
  cell (▏▎▍▌▋▊▉█), so a 25-cell bar shows half a percent; narrower windows
  get shorter bars. The part not filled is drawn in a shade a little
  lighter than the window's background (darker, on a light one), so the
  whole length shows without end marks
- goat's own windows (help, status, inventory, events) can be set up in
  `~/.config/goat/system.toml`: title, the key that shows them, palette,
  size and position (columns / rows or a percentage of the screen; a
  negative position counts from the right / bottom edge). goat writes the
  palette you choose for one of them, and where you move or resize it, back
  into that file. See [palettes.md](palettes.md)
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
| Hide / bring back all other windows | `Alt+0` again (background focused) |
| Move window               | `Alt+h/j/k/l` or `Alt+arrows`   |
| Resize window             | `Alt+H/J/K/L` or `Alt+Shift+arrows` |
| Pin / unpin (stay on top) | `Alt+w`                         |
| Choose the window's palette (colors) | `Alt+t`              |
| Scroll history by a page  | `Alt+PageUp` / `Alt+PageDown`   |
| Scroll history by 3 lines | `Alt+Shift+PageUp` / `PageDown` |
| Oldest line / live view   | `Alt+Home` / `Alt+End`          |
| Wrap long lines or not    | `Alt+z`                         |
| View left / right (no wrap) | `Alt+<` / `Alt+>`, Shift+wheel |
| Events window (bells, notifications, ...) | `Alt+-`         |
| Swap with the background  | `Alt+s`                         |
| Repaint the screen        | `Alt+r`                         |
| Settings (`local.toml`)   | `Alt+o`                         |
| Help: all keys and mouse actions | `F1` (again: hide)       |
| goat's status: version, memory, CPU | `F2` (again: hide)    |
| Inventory: every window; Enter goes to it | `F3` (again: hide) |
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

macOS Terminal also sends Option+←/→ as Esc b / Esc f, the same bytes as
Alt+b / Alt+f. In macOS Terminal goat reads them as Option+←/→ (move the
window), unless Option is sent as Meta (an Alt shortcut arrives as Esc +
letter): then they are Alt+b / Alt+f (`mac_option_arrows`). Elsewhere Esc b
/ Esc f are always Alt+b / Alt+f. iTerm2 sends real Alt+arrow keys.

**Teach goat your terminal: `goat -learn`.** It asks you to press eight
keys (Alt+c, Alt+b, Alt+f, the Alt+arrows and Alt+Shift+Left; Option on a
Mac), works out how this terminal sends them, and saves that in
`~/.config/goat/local.toml` under `[terminal."NAME"]`, one section per
terminal (macOS Terminal and iTerm2 each get their own). When two keys send
the same thing (macOS Terminal with *Use Option as Meta key*: Option+← and
Option+b), it asks which one you want. It also checks whether the terminal
draws the frame's 🧑 / 💻 two columns wide, and falls back to `user@host` if
not. From then on goat follows the profile in that terminal instead of
guessing (`mac_option_keys` / `mac_option_arrows` "auto"; explicit "on" /
"off" in config.toml still win). Run it again after changing the terminal's
keyboard settings.

If shortcuts don't work, run `goat -keys`: it shows which config file is in
use, whether `mac_option_keys` is on, and for each key you press the bytes
the terminal sends and what goat makes of them.

**Quitting** with floating windows open asks first: a "Quit goat?" box
lists them. `y` (or Alt+q again, or Enter on *Quit*) quits; `n`, `Esc` or
Enter on *Cancel* (the default) goes back. ←/→/Tab switch buttons, and
clicking one presses it. While the box is up, programs keep running and
their windows keep showing their output; only your keys, mouse and pastes
go to the box.
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
https://goat.nontechno.com/configs/ and the palettes,
`terminal-color-combinations.json`, from
https://color.nontechno.com/ (backup: the GitHub copy,
https://raw.githubusercontent.com/nontechno/color/refs/heads/main/),
then exits. A config file that can't be downloaded from there (or isn't a
valid file of its kind) comes from the backup copy at
https://raw.githubusercontent.com/nontechno/goat/refs/heads/resources/
(`configs/` folder) instead. Nothing is written unless every
download succeeds; a file you changed is kept as `<name>.<time>.bak`.
`GOAT_INSTALL_URL` points it at another server (used instead of both).

goat reads `$XDG_CONFIG_HOME/goat/config.toml` or
`~/.config/goat/config.toml`, and falls back to float's
`~/.config/float/config.toml`, so an existing float config keeps working. See
[`config.example.toml`](config.example.toml) for every option.

`~/.config/goat/local.toml` holds settings for this machine only (so
`config.toml` can be shared between machines); keep its keys above any
`[section]` goat adds to it:

| Key | Default | What it does |
|-----|---------|--------------|
| `show_clock`, `show_launch_host` | `true` | The clock and the host name at the right end of the status bar (re-read every few seconds) |
| `wrap_lines` | `config.toml`'s | Whether long lines wrap at start (background shell, launch script windows) |
| `new_window_size` | `config.toml`'s ratios | Size of new windows: `"100x30"` (whole window), `"60%x50%"`, or a mix |
| `confirm_quit` | `true` | Ask before Alt+q quits while floating windows are open |
| `exit_on_last_window` | `false` | Quit goat when the last floating window closes |

Alt+o opens a settings dialog for these: it reads `local.toml`, shows each
setting's value (or the default, when it isn't set), lets you change them
(↑↓ to choose, ←→ or Space to change a yes/no setting, Enter to type a
value; an empty value means the default), and on Save writes them back and
applies them at once; Discard (or Esc) leaves everything as it was. Saving
changes only these keys: comments, other keys and goat's sections stay.

goat also keeps the background window's colors (`[background_window]`) and
what `goat -learn` found out about the terminal (`[terminal."NAME"]`) there.

Mistakes in the config never stop goat: unknown keys and invalid values are
shown in the status bar and listed again when goat exits, and defaults are
used.

## Launch scripts

`goat -script FILE` (or `--script`) opens windows when goat starts, as FILE
describes. [scripting.md](scripting.md) has the full description, with
examples:

```
#!/usr/bin/env -S goat -script
# comments are whole lines starting with #
cd: ~/src/project
option: wrap: off

{
	cd: logs
	title: logs
	option: theme: dark
	run: tail -f app.log
}
{
	title: build
	option: focus: on
	run: make watch
}
run:
```

- `run:` opens a window running a command through your shell (empty: a
  shell), with the settings in effect at that line.
- Settings (`cd:`, `title:`, `option: NAME: VALUE`) hold from their line to
  the end of their block.
- `{` … `}` make a block: settings made inside apply only there and in
  blocks inside it.
- A relative `cd:` is taken from the folder the block started in; outside
  any block, from the script's folder.
- Values in quotes keep their spaces and braces.
- Options: `wrap`, `theme`, `bg`, `fg`, `frame`, `size`, `pos`, `pin`,
  `focus` and `hold`.
- A program that fails keeps its window open showing why; Enter closes it.

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
- Every key is sent: Home/End/PageUp/PageDown/Insert/Delete, F4–F12 (F1–F3
  open goat's help, status and inventory windows),
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
