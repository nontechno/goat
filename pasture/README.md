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
SIGKILL 3 s later if it is still running. The socket, lock and pid are then
removed. Stopping the server ends every program running on it.

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
go io.Copy(emu, p)        // output -> goat's vt emulator (io.EOF when it exits)
p.Write(keys)             // input, including the emulator's replies
p.Resize(w, h)
code, err := p.Wait()     // exit status

// goat exits: c.Close(). The programs keep running.

// Next time:
panes, _ := c.List()      // id, pid, argv, cwd, command, title, size, dead/status
p, _ := c.Attach(panes[0].ID, -1)
emu := vt.NewEmulator(p.Cols, p.Rows)
emu.Write(p.Snapshot)     // screen, scrollback, modes, title, cursor
p.Resize(w, h)            // then carry on as above
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
- **Output is always read**, attached or not, so programs never block on a
  full PTY. Output goes to a terminal emulator per pane (scrollback
  configurable, default 5000 lines) and to the attached clients.
- **Terminal queries** (device attributes, cursor position, ...) are answered
  by the server only while no client is attached. While one is attached,
  the client's emulator answers, so a program never gets two answers.
- **Input** is accepted only from clients attached to that pane. It goes
  through a queue, so a program that doesn't read never blocks the server.
- **Exit.** Attached clients get all remaining output, then an exit event
  with the status (128+n for signal n), and the pane is removed. If nobody
  was attached, the pane is kept, marked dead, and the next attach receives
  its last screen and then its exit.
- **Slow clients.** A client that falls 64 MB behind is disconnected rather
  than let memory grow.
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
- **debug:** every request with its duration, resizes, and sequences the
  emulator doesn't handle.

## Limits

- A snapshot taken while a full-screen program uses the alternate screen
  doesn't include the main screen underneath it. The scroll region is not
  restored either, but full-screen programs set it again when they redraw.
- The server's emulator doesn't reflow lines on resize.
- Unix only. On Windows, run the server and goat in WSL.
