# Integrating pasture into goat (instructions for a coding agent)

## Goal

Today goat runs each window's program on its own PTY (`window.go`,
`newWindow` → `pty.StartWithSize`), so every program dies when goat exits.
The goal is to offload those programs to the pasture mux server, a separate
long-lived process (`pasture/cmd/pasture`).

- **When goat quits:** its programs keep running on the server.
- **When goat starts:** it can reattach to them, with their screen,
  scrollback and modes restored.
- **When the server is off or unreachable:** goat behaves exactly as today.

Read these files first:

- `pasture/README.md`: behaviour, running it as a service, and the log.
- `pasture/client.go`: the API goat will call.
- `pasture/protocol.go`: the message types and the `PaneInfo` fields.
- `pasture/server_test.go`: working examples of every call.

## Constraints

- Keep goat working without pasture. It must be opt-in by config and fall
  back to local PTYs when the server can't be reached.
- Don't change `pasture/` unless it's required. If you must, keep
  `ProtocolVersion` in step and keep `go test -race ./pasture/` passing.
- Goat's event loop is single-threaded: everything in `WM` and `Window` is
  owned by it. **Never block it on a pasture round trip that can wait.**
  Client calls (`Spawn`, `Attach`, `Resize`, `List`, `Kill`, `Detach`) block
  until the server answers and have no timeout. Run them from goroutines and
  deliver results back through channels, as `ptyMsg` does. `Pane.Write`
  can block too: it waits while 1 MiB of the pane's input is not yet read
  by the program (flow control, protocol v3). Call it only from the
  goroutine that drains `win.in`, never from the loop.
- `Pane.Read` must be called continuously while attached. Unread output is
  not buffered without limit: once about 1 MiB is unread, the server stops
  reading the program's PTY, for every client attached to that pane. The
  reader goroutine that forwards to `ptyMsg` already does this; don't make
  it wait on the loop for long. Acknowledgements are sent by `Read`
  automatically.
- Unix only, like goat.
- Follow the repo's style. Changes to vendored code are marked `GOAT PATCH`,
  and tests sit next to the code they cover.

## The pasture API (package `github.com/nontechno/goat/pasture`)

```go
sock, err := pasture.DefaultSocket()          // ($PASTURE_TMPDIR or /tmp)/pasture-<uid>/default
c, err := pasture.Dial(sock, "goat")          // handshake; error on version mismatch
c.Close()                                      // disconnect; programs keep running
<-c.Done(); c.Err()                            // connection ended, and why

p, err := c.Spawn(pasture.SpawnOptions{
    Argv: argv,        // e.g. strings.Fields(shell); argv[0] looked up in the PATH from Env
    Env:  env,         // complete environment, KEY=VALUE, must include TERM
    Dir:  dir,         // ABSOLUTE, or "" for "/"
    Cols: cw, Rows: ch,
    History: n,        // history lines kept in the server's memory (0 = 2000, <0 = no history);
                       // older lines go to disk on the server (pasture -history-max)
})                     // the client is attached to the new pane

infos, err := c.List()     // []PaneInfo: ID, PID, Argv, Dir, Cwd, Command, Title,
                           //   Cols, Rows, Attached, Dead, Status, Created,
                           //   HistoryOldest, HistoryEnd, HistoryDiskBytes,
                           //   Paused (output held back), InputQueued (bytes the program hasn't read)
p, err := c.Attach(id, n)  // snapshot with the last n history lines (-1 = as many as fit, ~8 MB);
                           //   p.Snapshot, p.Cols, p.Rows, p.HistoryFirst, p.HistoryOldest
page, err := c.History(id, before, count)
                           // up to count lines just before line `before` (pasture.HistoryEnd = newest),
                           //   oldest first: page.First, page.Lines, page.Oldest, page.End;
                           //   next page: before = page.First, while page.More()
err = c.Kill(id)           // hang up any pane

// *pasture.Pane (p.ID, p.PID) behaves like the PTY master:
n, err := p.Read(buf)  // output; io.EOF after the program exits and output is drained;
                       // pasture.ErrClosed (or the connection error) after Detach or connection loss
p.Write(b)             // input (keys, pastes, and the emulator's replies); may block (flow control)
p.Resize(cols, rows)   // TIOCSWINSZ on the server; the program gets SIGWINCH
p.Signal(sig)          // to the foreground process group
p.Kill()               // SIGHUP to the process group, SIGKILL after 3s; exit follows
p.Detach()             // stop receiving; the program keeps running
code, err := p.Wait()  // exit status (128+n for signal n); err != nil = not an exit
<-p.Exited()
```

Server guarantees that goat can rely on:

- All output is delivered before the exit, and none is dropped: a client
  that reads slowly slows the program down instead (flow control).
- If an attach snapshot with the requested history would be too large for
  one message, it comes without history (`p.HistoryFirst` is then the end);
  page the history with `History` instead.
- Input is accepted only from a client attached to the pane.
- While goat is attached, the server does NOT answer terminal queries
  (DA/DSR/CPR). Goat's emulator must answer, which it already does:
  `io.Copy(win.in, win.emu)` sends replies back as input. That stays as is.
- A pane that exited while nobody was attached shows up in `List` with
  `Dead: true, Status`. `Attach` returns its last screen, and `Wait` then
  returns immediately; the server forgets it after that.
- If several clients are attached, the last `Resize` wins.

## Where goat touches the PTY today

Everything is in `window.go` unless noted.

- `Window` fields `ptmx *os.File`, `cmd *exec.Cmd`, `in *inputQueue`,
  `exited atomic.Bool`.
- `newWindow(id, shell, x, y, w, h, winOpts, out chan<- ptyMsg)`:
  1. `exec.Command` → `pty.StartWithSize` → `pollable`.
  2. Creates `vt.NewEmulator(emuWidth(cw), ch)` and registers the callbacks
     (Title, WorkingDirectory, CursorVisibility, CursorStyle,
     Enable/DisableMode, OSC 52).
  3. Starts `go io.Copy(win.in, win.emu)` and `go win.in.drainTo(ptmx)`.
  4. Starts a reader goroutine that sends `ptyMsg{data}` and a wait
     goroutine that sends `ptyMsg{exited: true}`.
- `setGeometry`, and `setWrap` via `emu.Resize`: `setWinsize(w.ptmx, cw, ch)`.
  In no-wrap mode the emulator is wider than the PTY (`emuWidth`), while the
  PTY/program size is always `contentW() × contentH()`.
- `close()`: closes the emulator's reply pipe, `in`, and `ptmx`, then sends
  SIGHUP to the process group and SIGKILL after 3s.
- `refreshProcName()` / `currentDir()` call `foregroundProcessName` /
  `foregroundDir(ptmx, cmd)` from `procname_linux.go`, which need the PTY
  fd.
- `wm.go`:
  - `handlePty(msg)`: on exit it logs `w.cmd.ProcessState.String()`, calls
    `m.remove(w)`, and for the background window calls
    `backgroundExited`.
  - `remove(w)` → `w.close()`.
  - `closeAll()` on quit.
  - `startBackground()` and `newWindow()` create windows.
  - `tick()` polls process names and directories twice a second.
- `main.go`: the loop reads `m.out` (`ptyMsg`). `m.closeAll()` runs on exit.

## Recommended design

### 1. A backend interface for a window's program

Put this in a new file, e.g. `backend.go`. Both kinds of program implement
it, so the rest of `Window` doesn't care where its program runs.

```go
type backend interface {
    io.Reader                 // program output (the reader goroutine reads this)
    io.Writer                 // input (win.in drains into this)
    Resize(cols, rows int)    // must not block the event loop (see 4)
    Hangup()                  // user closed the window: end the program
    Leave()                   // goat is quitting: local = Hangup; remote = Detach
    Wait() (status string)    // blocks until exit; used by the wait goroutine
    Pid() int
    ProcName() string         // foreground process name ("" if unknown)
    Dir() string              // foreground process cwd ("" if unknown)
}
```

- `localBackend` wraps today's `ptmx`/`cmd` code. Move it out of
  `newWindow`, `close`, `setWinsize`, `foregroundProcessName` and
  `foregroundDir` without changing behaviour.
- `remoteBackend` wraps `*pasture.Pane`. `ProcName` and `Dir` return values
  cached from the last `List` (see 5). `Wait` formats `p.Wait()` as
  "exit status N" or "signal N". If Wait returns an error, meaning the
  connection was lost, it returns "lost connection to pasture".

`Window` keeps `emu`, `in` and all emulator callbacks unchanged. Only
`ptmx` and `cmd` are replaced by `be backend`. The reader goroutine becomes
`be.Read` → `out <- ptyMsg{data}`. On `io.EOF`, `ErrClosed` or any other
error, it stops, and the wait goroutine sends
`ptyMsg{exited: true, status: be.Wait()}`. Add a `status string` field to
`ptyMsg` and use it in `handlePty` instead of `w.cmd.ProcessState`.

### 2. Connection management (in `WM`)

- Keep one `*pasture.Client` for the whole session: `m.pasture`, nil when
  pasture isn't used.
- At start, when enabled, `Dial`. If that fails with
  `ENOENT`/`ECONNREFUSED` and `autostart` is on:
  1. Run `<binary> -d -S <sock>`.
  2. Find the binary from the config, else `pasture` next to
     `os.Executable()`, else in `$PATH`.
  3. Wait for the command to exit 0; it prints pid, socket and log. If the
     user runs pasture as a systemd/launchd service (`pasture/service/`),
     autostart is never needed; prefer that.
  4. Dial again.

  If it still fails, show `setStatus("pasture: <err>; using local
  terminals")` and continue locally.
- A version-mismatch error (the message contains "protocol version
  mismatch") means goat and pasture are from different builds. Show it and
  fall back to local PTYs. Don't kill the server automatically, because that
  would kill the user's programs.
- Watch `c.Done()` in a goroutine that posts to the loop. On loss:
  - set `m.pasture = nil` and show a status message;
  - remote windows get their exit through their wait goroutines, and are
    removed like any exited window;
  - new windows use local PTYs until restart. Don't try to reconnect inside
    a session.

### 3. Spawning

In `newWindow` (`window.go`), when `m.pasture != nil`:

- `argv := strings.Fields(shell)`, `env := childEnv()` (already sets
  `TERM=xterm-256color` and `COLORTERM`).
- `dir := o.dir`; if it is empty or relative, use `os.Getwd()`. It must be
  absolute.
- Cols/Rows = `contentW()`/`contentH()` (the PTY size, not `emuWidth`).
- `Spawn` blocks for about a millisecond. That's acceptable at window
  creation (a user action), but log how long it took. If `Spawn` fails, log
  it and fall back to a local PTY for that window.

### 4. Resizing without blocking

`setGeometry` runs on every mouse-drag step. Give each `remoteBackend` a
goroutine with a channel of capacity 1 that holds the latest size.

- `Resize` stores the size, replacing any pending one, and never waits.
- The goroutine calls `p.Resize` with the latest value.
- On errors, log at debug level and ignore them.

### 5. Process name and directory

`tick()` calls `refreshProcName`/`refreshDir` twice a second. For remote
windows, run one `c.List()` per tick in a goroutine and post the result to
the loop. At most one should be in flight; skip the tick if the last hasn't
returned. Then set each window's cached `Command` and `Cwd` by pane ID.
`PaneInfo.Title` also has the program-set title, but goat already gets it
from its own emulator.

### 6. Closing versus quitting

- The user closes a window (`remove` → `close`): call `be.Hangup()`, which
  is `p.Kill()` for remote panes, run in a goroutine. The exit arrives
  through `Wait`. `remove` already dropped the window, so `handlePty`
  ignores it.
- goat quits (`closeAll` on exit): call `be.Leave()`, which is `p.Detach()`
  for remote windows. Then `c.Close()`. Split `closeAll` so that quitting
  doesn't hang up remote programs. Keep the ~500 ms total bound on exit:
  don't wait on unresponsive calls.
- Background window (window 0): decide with the user. The simplest approach
  that matches today's behaviour is to always run it locally, and offload
  only floating windows. If it is offloaded, restore it as the background on
  reattach (see 7).

### 7. Reattaching on start (`restore`)

After `Dial`, `List()`. For every pane with `Attached == 0`:

1. `p, err := c.Attach(info.ID, -1)`.
2. Create the Window as in `newWindow`, but with `be = remoteBackend(p)`.
   Create the emulator with `vt.NewEmulator(p.Cols, p.Rows)`, set the
   scrollback size, and register ALL callbacks first (so the snapshot's
   title, modes, cursor style and visibility update goat's fields
   `appCursor`, `mouseModes`, `cursorVisible`, `cursorShape`/`cursorBlink`
   and `oscTitle`). Then `emu.Write(p.Snapshot)` — or `w.feed(p.Snapshot)`,
   which recovers from emulator panics. Only then apply host colors
   (`applyHostColors`).
3. Start the reader and wait goroutines as for spawned panes. Output that
   arrived after the snapshot is already buffered in `p` and is read next,
   so nothing is lost.
3a. History older than the snapshot (`p.HistoryFirst > p.HistoryOldest`)
   stays on the server. Attach with a modest count (e.g. the window's
   scrollback_lines) to keep attaching fast. When the user scrolls back past
   the start of the emulator's scrollback, fetch older lines **in a goroutine**:
   `c.History(id, before, n)`, with `before` = the number of the oldest line
   goat has, starting at `p.HistoryFirst`. Post them to the loop. Keep them
   in a separate per-window list in front of the emulator's scrollback; the
   vt emulator can't prepend history.

   Each line is text with SGR escape sequences. To draw one, parse it into
   cells, e.g. by writing it into a 1-row scratch `vt.Emulator` of the
   window's width. Line numbers are absolute and stable, so a page can
   never overlap or skip lines. Stop at `!page.More()`, and show "older
   history discarded" if `page.Oldest > 0`. Everything before
   `p.HistoryFirst` is older than what goat already holds (the snapshot and
   later output), so pages fit in front of goat's scrollback without
   overlap. Don't try to match goat's own later scrollback lines to server
   numbers: wrapping differs, e.g. in no-wrap mode.
4. Place the window. The simplest way is to cascade like `newWindow`. To
   restore exact geometry, colors, frame and background status, keep a small
   state file keyed by `(pane ID, PID, Created)`, e.g.
   `$XDG_STATE_HOME/goat/pasture.json`, written on quit. Pane IDs are
   unique only within one server lifetime, hence the PID/Created check.
5. `setGeometry` → `emu.Resize(emuWidth(cw), ch)`, then `be.Resize(cw, ch)`.
   The snapshot is laid out for the old size; the program redraws on
   SIGWINCH.
6. `Dead: true` panes: attach them too, which shows the last screen. Their
   `Wait` returns at once and they close like any exited window. Show
   `setStatus("window N had exited (status S)")`. Alternatively, `Kill`
   them without showing.

Skip panes with `Attached > 0`: another goat owns them.

### 8. Config (`config.go`, `config.example.toml`, README)

Add, off by default:

```toml
[pasture]
enabled   = false   # run programs on the pasture server so they survive goat
socket    = ""      # "" = pasture.DefaultSocket()
autostart = true    # start "pasture -d" when no server is running
binary    = ""      # path to pasture; "" = next to goat, then $PATH
restore   = true    # reattach to detached programs at start
history   = 5000    # server-side scrollback per program (use scrollback if set)
```

Validate them like the other options, with warnings through `warns`. Also:

- Update `config_test.go` / `example_config_test.go`.
- Document the option in the README, together with how to stop the server
  (`kill <pid>`, or `systemctl --user stop pasture`). Stopping the server
  ends every program on it.

### 9. Logging (goat side)

Use goat's `logger` for:

- info: dial success/failure with the socket path; autostart with the
  pid/log line pasture printed; attach/restore of each pane (id, pid,
  command); detach on quit; connection lost.
- debug: every Spawn/Attach/List with its duration.

pasture logs its own side to `<socket dir>/pasture.log` (or the journal
under systemd). Mention both logs in goat's README for troubleshooting.

## Tests to add (goat)

Run a real server in-process, as `pasture/server_test.go`'s `startServer`
does:

```go
srv, _ := pasture.NewServer(pasture.Config{Socket: filepath.Join(t.TempDir(), "s")})
go srv.Serve()
t.Cleanup(func() { srv.Shutdown("test") })
```

`t.TempDir()` is mode 0700, as required. If the path is too long for a Unix
socket (over ~100 bytes), use `os.MkdirTemp("", "p")`.

Cover:

1. A window created with pasture shows program output and receives typed
   keys, and `Resize` reaches the program (`stty size`).
2. Closing the window kills the program; `List` becomes empty.
3. "Quit" (the `closeAll` path) leaves the program running (`List` shows it,
   `Attached == 0`). A new WM with `restore` reattaches: the emulator shows
   the earlier output, and the cursor and modes are restored, e.g. after
   `printf '\e[?2004h'` bracketed paste is on in the new window.
4. The server stopping while goat runs: windows close with a status message,
   and new windows fall back to local PTYs.
5. The server unreachable at start: goat starts with local PTYs and a
   warning.
6. Resizing during a drag doesn't block the loop, and the last size wins.
7. A large paste into a program that isn't reading (`sleep 5; cat`) doesn't
   block the loop, and arrives complete once the program reads.
8. A program with a lot of output (`yes | head -n 1000000`) is shown in
   full, and the loop stays responsive while it runs.

Then run `go vet ./...` and `go test -race ./...`, and check by hand in a
real terminal: vim/htop in an offloaded window survive quitting goat and
come back intact.

## Known limits to keep in mind

- If a full-screen program is running on the alternate screen at reattach,
  the main screen underneath it isn't restored. After the program exits,
  the shell shows its prompt on a mostly blank screen; the scrollback is
  intact.
- The server's emulator doesn't rewrap lines on resize; goat's own
  emulator behaves the same today.
- The server has no per-pane metadata such as tags or geometry. Keep goat's
  window layout in goat's own state file (7.4).
