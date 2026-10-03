# pasture

A small mux server for goat. It is a background process that owns
pseudo-terminals and the programs running on them, so those programs keep
running when goat exits. The next goat can reattach to them.

There is no UI and no terminal client. goat talks to the server through this
package (`pasture.Dial`), and the server writes a detailed log.

## Run

```
go build -o ~/go/bin/pasture ./pasture/cmd/pasture

pasture -d              # start in the background (daemon); prints pid, socket and log
pasture                 # run in the foreground; this is the mode service managers use
```

Flags:

| Flag | Meaning |
|---|---|
| `-S path` | socket path |
| `-log file` | log file; `-` = stderr |
| `-log-level level` | `debug`, `info` (default), `warn` or `error` |
| `-d` | daemonize |
| `-version` | print the protocol version |
| `-history-max size` | history kept on disk per pane (`K`/`M`/`G`; default `64M`; `0` = none) |
| `-history-dir dir` | where that history goes (default: `<socket>.history`) |

The default socket is `/tmp/pasture-<uid>/default`, or
`$PASTURE_TMPDIR/pasture-<uid>/default`. `$TMPDIR` is not used, because it
differs between a service manager, a terminal and an SSH login.

### As a daemon (`-d`)

`pasture -d` does the following:

1. Runs itself again in a new session (`setsid`): it has no controlling
   terminal, so it gets no SIGHUP when your terminal or SSH session ends.
2. Puts its stdio on `/dev/null` and makes `/` its working directory.
3. Returns only once the server accepts connections. If the server fails to
   start, it prints why and exits 1.

The log goes to `<socket dir>/pasture.log` unless `-log` says otherwise.

Each server holds a lock on its socket, so only one can run per socket. If
one is already running, `pasture -d` reports "already running" and exits 0,
which makes it safe to call from a login script or from goat. A second
foreground `pasture` on the same socket exits with an error. The lock file doubles as a pid
file:

```
kill "$(cat /tmp/pasture-$(id -u)/default.lock)"     # stop (SIGTERM)
kill -HUP "$(cat /tmp/pasture-$(id -u)/default.lock)" # reopen the log (logrotate)
```

SIGTERM or SIGINT stops the server cleanly. Every program gets SIGHUP, and
SIGKILL 3 s later if it is still running; the server waits for that before
it exits. The socket is then removed, and the lock file emptied (it stays,
so a server starting at that moment can't end up running beside another).
Stopping the server ends every program running on it.

### As a service

Ready-made files are in `service/`:

**systemd (Linux, including WSL), user service.** The full guide is
[`service/SYSTEMD.md`](service/SYSTEMD.md). In short:

```
cd pasture/service && bash install-systemd.sh --linger   # build, install, enable, start
journalctl --user -u pasture -f                       # log
systemctl --user stop pasture                         # stop (ends its programs)
```

**launchd (macOS), user agent:**

```
cp service/com.nontechno.pasture.plist ~/Library/LaunchAgents/   # edit the pasture path
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.nontechno.pasture.plist
launchctl bootout gui/$(id -u)/com.nontechno.pasture              # stop
```

The agent starts at login, restarts after a crash, and logs to
`/tmp/pasture-launchd.log`.

**Anything else** (runit, s6, supervisord, a login script): run `pasture` in
the foreground and send SIGTERM to stop it.

## Use from goat

```go
c, err := pasture.Dial(sock, "goat")      // sock: pasture.DefaultSocket()
p, err := c.Spawn(pasture.SpawnOptions{
    Argv: []string{"/bin/bash", "-l"}, Env: env, Dir: dir, Cols: w, Rows: h,
})
// p behaves like the PTY master:
go io.Copy(emu, p)        // output -> goat's vt emulator (io.EOF when it exits);
                          // keep reading: unread output holds the program back
go p.Write(keys)          // input, including the emulator's replies; Write
                          // waits while the program doesn't read its input
p.Resize(w, h)
code, err := p.Wait()     // exit status

// goat exits: c.Close(). The programs keep running.

// Next time:
panes, _ := c.List()      // id, pid, argv, cwd, command, title, size, dead/status
p, _ := c.Attach(panes[0].ID, -1)
emu := vt.NewEmulator(p.Cols, p.Rows)
emu.Write(p.Snapshot)     // screen, recent history, modes, title, cursor
p.Resize(w, h)            // then carry on as above

// Older history, page by page (newest first):
before := p.HistoryFirst  // the snapshot holds lines from here on
for {
    page, err := c.History(p.ID, before, 500)
    if err != nil || len(page.Lines) == 0 { break }
    show(page.First, page.Lines) // lines page.First.. page.First+len-1, oldest first
    if !page.More() { break }
    before = page.First
}
```

`List` also gives each pane's current directory and foreground command
(Linux, from `/proc`). goat can use them for titles and new-window
directories.

## Behaviour

- **One server per socket.** The server holds a `flock` on `<socket>.lock`,
  and a socket left by a crashed server is replaced.
- **Access.** The socket directory is 0700 and must belong to the user; the
  socket itself is 0600. Each connection's peer UID is checked
  (`SO_PEERCRED` / `LOCAL_PEERCRED`), and only the server's own user is
  accepted.
- **Handshake.** The protocol version is checked first, so a client and a
  server from different builds refuse each other with a clear error.
- **Spawn.** Each program gets its own session with the PTY as its
  controlling terminal. The size is set before it starts, and argv, env and
  dir are exactly what the client sent (dir must be absolute).
- **Output** goes to a terminal emulator per pane and to the attached
  clients. With nobody attached it is always read, so a program never
  blocks on a full PTY.
- **Flow control.** Clients acknowledge output as `Pane.Read` returns it.
  While an attached client has 1 MiB or more of a pane's output unread,
  the server stops reading that pane: the program waits, as it would on a
  slow terminal, and nothing is lost. A client that stops reading
  therefore slows the pane for every client attached to it; detaching it
  (or its disconnect) lets the pane run again. `List` reports `paused`.
- **History.** Every line that scrolls off a pane's screen gets a number
  (from 0) that never changes. The newest lines (`SpawnOptions.History`,
  default 2000) stay in memory. Older ones are moved to append-only files,
  `<socket>.history/<pane>-<n>.log`, so memory use stays small however long
  the history gets. Each line is stored as text with its colors as escape
  sequences.
  - **Limit:** at `-history-max` per pane (64 MB by default), the oldest
    files are deleted.
  - **Clearing:** a clear of the scrollback (`clear`, ED 3) discards the
    history, on disk too, as a terminal would.
  - **Cleanup:** the files are deleted with the pane, and leftovers from a
    crashed server are deleted at start.
  - **Snapshot:** an attach snapshot carries recent history, up to about
    8 MB. If a snapshot would still be too large for one frame, it is sent
    without history (which can then be paged).
  - **Paging:** `History(pane, before, count)` pages back through the rest,
    up to 10,000 lines or 4 MB per page. Paging is stable while the program
    keeps writing, because line numbers don't shift.
- **Terminal queries** (device attributes, cursor position, ...) are answered
  by the server only while no client is attached. While one is attached,
  the client's emulator answers, so a program never gets two answers.
- **Sizes.** Panes are at most 1000×1000 cells; memory history is at most
  100,000 lines per pane.
- **Input** is accepted only from clients attached to that pane. It goes
  through a queue, so a program that doesn't read never blocks the server.
  The queue is bounded: each client may have 1 MiB of a pane's input
  unwritten, and `Pane.Write` waits for the server's acknowledgements
  beyond that. A client that sends past the limit is disconnected. If
  writing to the program fails, its later input is dropped (and logged).
  `List` reports `input_queued`.
- **Exit.** Attached clients get all remaining output, then an exit event
  with the status (128+n for signal n), and the pane is removed. If nobody
  was attached, the pane is kept, marked dead, and the next attach receives
  its last screen and then its exit.
- **Slow clients.** Apart from flow control, a client whose connection
  falls 64 MB behind (responses included) is disconnected rather than let
  memory grow.
- **Failures are contained.** A panic while handling a message or in a pane's
  emulator is logged with a stack trace, and the server keeps running.
  Malformed frames close only that connection.
- **Resizing.** Several clients may attach to one pane; the last resize wins.

## Log

The log is text key=value (`log/slog`).

- **info:** server start and stop, client connect and disconnect (name, pid,
  duration, reason), pane spawn (argv, dir, size, pid), attach and detach,
  signals, kills, exits (status, run time, bytes in and out).
- **warn:** refused connections, protocol errors, requests that failed.
- **error:** failures and panics.
- **debug:** every request with its duration, resizes, output paused and
  resumed by flow control, and sequences the emulator doesn't handle.

## Limits

- A snapshot taken while a full-screen program uses the alternate screen
  doesn't include the main screen underneath it. The scroll region is not
  restored either, but full-screen programs set it again when they redraw.
- The server's emulator doesn't reflow lines on resize.
- Unix only. On Windows, run the server and goat in WSL.
