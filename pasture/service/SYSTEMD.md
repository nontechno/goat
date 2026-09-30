# Running pasture with systemd

pasture is the mux server that keeps goat's programs running. This guide
installs it as a **systemd user service** under your own account. It starts
at login (or at boot, with linger), restarts if it crashes, and logs to the
journal. It must run as your own user, not root, because the programs it
holds are yours.

The files in this directory:

| File | Purpose | Installed to |
|---|---|---|
| `install-systemd.sh` | Builds, installs, starts, upgrades and uninstalls | (run it from here) |
| `pasture.service` | The systemd unit | `~/.config/systemd/user/pasture.service` |
| `pasture.env` | Settings (log level, socket directory) | `~/.config/pasture/pasture.env` |
| (built from `pasture/cmd/pasture`) | The server binary | `~/.local/bin/pasture` |

Runtime paths, for user id `$(id -u)`:

| Path | What |
|---|---|
| `/tmp/pasture-<uid>/` | Socket directory, mode 0700 |
| `/tmp/pasture-<uid>/default` | The socket goat connects to (mode 0600) |
| `/tmp/pasture-<uid>/default.lock` | Lock file, containing the server's pid |
| `journalctl --user -u pasture` | The log |

---

## 1. Quick start

```bash
cd ~/go/src/github.com/nontechno/goat/pasture/service   # or wherever the goat repo is
bash install-systemd.sh --linger   # "bash": the checkout may have lost the executable bit
```

The script:

1. Checks that it is on Linux, not running as root, and that your systemd
   user manager is reachable. On WSL it explains how to enable systemd if
   it isn't.
2. Builds the server from the repository:
   `go build -trimpath ./pasture/cmd/pasture`.
3. Installs the binary, unit and settings file listed above. An existing
   settings file is kept.
4. Runs `systemctl --user daemon-reload`, `enable` and `start`.
5. With `--linger`, runs `loginctl enable-linger $USER`, so the service
   starts at boot and keeps running after you log out.
6. Waits for the socket to appear and prints the pid and socket path.

Expected output (paths vary):

```
==> building pasture from /home/sport/go/src/github.com/nontechno/goat
==> installing /home/sport/.local/bin/pasture
==> installing /home/sport/.config/systemd/user/pasture.service
==> installing settings /home/sport/.config/pasture/pasture.env
==> starting pasture
==> enabling linger for sport (service keeps running after logout)
==> pasture is running: pid 12345, socket /tmp/pasture-1000/default
==> logs: journalctl --user -u pasture -f
```

Other options:

```bash
./install-systemd.sh --dry-run            # show what it would do
./install-systemd.sh --bin /path/pasture  # install a prebuilt binary (no Go needed)
./install-systemd.sh --restart            # upgrade and restart now (ENDS all its programs)
./install-systemd.sh --uninstall          # stop, disable, remove the unit
./install-systemd.sh --uninstall --purge  # ... and the binary, settings and log
```

---

## 2. Prerequisites

### 2.1 Linux with systemd

Check that your systemd user manager is running:

```bash
systemctl --user status          # should print a tree, not "Failed to connect to bus"
echo "$XDG_RUNTIME_DIR"          # should be /run/user/<uid>
```

You need a direct login as this user: a desktop session, SSH, or a WSL
terminal. After `su` or `sudo -u`, `systemctl --user` usually can't reach
the user manager.

### 2.2 Go (only to build)

Go 1.24.2 or newer, the same as goat:

```bash
go version
```

If you'd rather not install Go on the target machine, build elsewhere with
`GOOS=linux GOARCH=amd64 go build -o pasture ./pasture/cmd/pasture` (from
the repo root) and install with `--bin`.

### 2.3 WSL (Windows)

The goat sources are at `D:\go\src\github.com\nontechno\goat`, which is
`/mnt/d/go/src/github.com/nontechno/goat` inside WSL. Both pasture and goat
must run inside the same WSL distribution; Windows programs can't use the
socket.

**a) Enable systemd in the distribution.** In WSL:

```bash
sudo tee /etc/wsl.conf >/dev/null <<'EOF'
[boot]
systemd=true
EOF
```

Merge this by hand if `/etc/wsl.conf` already has other settings. Then, in
PowerShell:

```powershell
wsl --shutdown
```

Open WSL again and check with `systemctl --user status`.

**b) Keep the distribution running.** By default WSL shuts a distribution
down about 15 s after its last terminal closes, and that would stop pasture
and every program on it. To prevent it, create or edit
`%UserProfile%\.wslconfig` on Windows:

```ini
[general]
instanceIdleTimeout=-1   # never shut the distribution down when idle

[wsl2]
vmIdleTimeout=-1         # Windows 11: never shut the VM down when idle
```

Then run `wsl --shutdown` once more for the settings to take effect. Both
settings are described in Microsoft's
[WSL configuration docs](https://learn.microsoft.com/windows/wsl/wsl-config).
Even then, a Windows restart or `wsl --shutdown` ends everything.

**c) Start WSL when you log in to Windows (optional).** Create a Task
Scheduler task "At log on" that runs:

```
wsl.exe -d Ubuntu --exec /bin/true
```

Replace `Ubuntu` with your distribution's name from `wsl -l`. systemd then
starts pasture, provided the service is enabled and linger is on.

**d) Build location.** Building from `/mnt/d/...` works but is slow. To
build faster, clone or copy the repo into the Linux filesystem, e.g.
`~/src/goat`, and run the script from there.

---

## 3. Manual installation (what the script does)

From the goat repository root:

```bash
# 1. Build and install the binary
go build -trimpath -o /tmp/pasture ./pasture/cmd/pasture
install -Dm755 /tmp/pasture ~/.local/bin/pasture
~/.local/bin/pasture -version                 # prints: pasture protocol 1

# 2. Unit and settings
install -Dm644 pasture/service/pasture.service ~/.config/systemd/user/pasture.service
install -Dm600 pasture/service/pasture.env     ~/.config/pasture/pasture.env

# 3. Enable and start
systemctl --user daemon-reload
systemctl --user enable --now pasture.service

# 4. Optional: keep it running without a login session, and start it at boot
loginctl enable-linger "$USER"                # may need: sudo loginctl enable-linger "$USER"

# 5. Check
systemctl --user status pasture
ls -l /tmp/pasture-$(id -u)/
```

Add `~/.local/bin` to `PATH` in `~/.profile` or `~/.bashrc` if it isn't
there. goat looks for `pasture` there when it starts the server itself.

```bash
export PATH="$HOME/.local/bin:$PATH"
```

---

## 4. Everyday use

| Task | Command |
|---|---|
| Status | `systemctl --user status pasture` |
| Is it running? | `systemctl --user is-active pasture` |
| Follow the log | `journalctl --user -u pasture -f` |
| Log since boot | `journalctl --user -u pasture -b` |
| Last hour, warnings and up | `journalctl --user -u pasture --since -1h -p warning` |
| Start | `systemctl --user start pasture` |
| Stop (**ends all its programs**) | `systemctl --user stop pasture` |
| Restart (**ends all its programs**) | `systemctl --user restart pasture` |
| Start at login / boot | `systemctl --user enable pasture` (+ linger for boot) |
| Don't start automatically | `systemctl --user disable pasture` |
| Server pid | `cat /tmp/pasture-$(id -u)/default.lock` or `systemctl --user show -p MainPID pasture` |
| Programs it holds | `systemd-cgls --user-unit pasture.service` |

Stopping or restarting the server hangs up every program running on it
(SIGHUP, then SIGKILL after 3 s). Quitting goat does not stop anything; the
programs keep running and goat reattaches to them.

### What the log looks like

```
level=INFO msg="server started" pid=12345 uid=1000 socket=/tmp/pasture-1000/default protocol=1
level=INFO msg="client connected" client=1 client_name=goat client_pid=23456
level=INFO msg="pane spawned" client=1 pane=1 pid=23460 argv=[/bin/bash -l] dir=/home/sport size=120x40
level=INFO msg="client disconnected" client=1 reason="connection closed" connected_for=2h13m
level=INFO msg="pane attached" client=2 pane=1 snapshot_bytes=48211
level=INFO msg="pane exited" pane=1 pid=23460 status=0 bytes_out=1832201 bytes_in=5120 ran=5h2m
```

For more detail (every request and its timing), set
`PASTURE_ARGS=-log-level=debug` (section 5) and restart.

---

## 5. Configuration

### 5.1 `~/.config/pasture/pasture.env`

```bash
PASTURE_ARGS=-log-level=info    # extra flags: -log-level=debug|info|warn|error, -S=/path/socket
#PASTURE_TMPDIR=/tmp            # socket directory base (socket: $PASTURE_TMPDIR/pasture-<uid>/default)
```

After editing, run `systemctl --user restart pasture`, which ends its
programs.

If you change the socket (`PASTURE_TMPDIR` or `-S`), goat must use the same
one. Export the same `PASTURE_TMPDIR` in your shell profile, or set goat's
socket option.

### 5.2 Overriding the unit (drop-ins)

Don't edit the installed unit; the installer overwrites it on upgrade. Use
a drop-in instead:

```bash
systemctl --user edit pasture
```

Examples:

```ini
# The binary is somewhere else, e.g. ~/go/bin
[Unit]
ConditionFileIsExecutable=
ConditionFileIsExecutable=%h/go/bin/pasture
[Service]
ExecStart=
ExecStart=%h/go/bin/pasture -log - $PASTURE_ARGS
```

```ini
# Programs started through pasture get a larger file-descriptor limit
[Service]
LimitNOFILE=65536
```

Then run `systemctl --user daemon-reload && systemctl --user restart pasture`.
`systemctl --user cat pasture` shows the effective unit.

### 5.3 Why the unit has no sandboxing

The unit deliberately sets no `PrivateTmp`, `ProtectHome`,
`NoNewPrivileges`, `UMask` or resource limits. Every program you run in
goat's windows runs inside this service and inherits its settings:
`NoNewPrivileges` would break `sudo`, `PrivateTmp` would hide the socket in
`/tmp` from goat, and a `UMask` would change the permissions of files you
create in your shells. pasture protects its own socket: the directory must
be 0700 and owned by you, the socket is 0600, and each connection's user id
is checked.

---

## 6. Upgrading

1. Pull and rebuild: `./install-systemd.sh`. The new binary replaces the
   old one safely while the old one is running.
2. The running server keeps using the old binary until it restarts, which
   ends all its programs. Choose a good moment, then run
   `systemctl --user restart pasture` (or `./install-systemd.sh --restart`).
3. goat and pasture must speak the same protocol version. After an upgrade
   that changed it, goat reports "protocol version mismatch" until pasture
   restarts; `pasture -version` prints the version.

---

## 7. Uninstalling

```bash
./install-systemd.sh --uninstall           # stop + disable + remove the unit
./install-systemd.sh --uninstall --purge   # also ~/.local/bin/pasture, settings, log
loginctl disable-linger "$USER"            # if nothing else needs linger
```

Manually:

```bash
systemctl --user disable --now pasture
rm ~/.config/systemd/user/pasture.service
systemctl --user daemon-reload
rm -f ~/.local/bin/pasture; rm -rf ~/.config/pasture
```

---

## 8. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `Failed to connect to bus: No medium found` (or `No such file or directory`) | No user manager. Log in directly as the user (not `su`/`sudo -u`), or `export XDG_RUNTIME_DIR=/run/user/$(id -u)`. On WSL, enable systemd (2.3a). |
| `status` shows `Condition: start condition failed ... ConditionFileIsExecutable` | The binary isn't at `~/.local/bin/pasture`. Install it, or point a drop-in at the right path (5.2). |
| Log: `cannot start err="another server is running for ..."` | A pasture started by hand (`pasture -d`) holds the socket. Stop it with `kill "$(cat /tmp/pasture-$(id -u)/default.lock)"`, then `systemctl --user restart pasture`. |
| Log: `socket directory: ... is accessible by others` or `owned by uid N` | Someone created `/tmp/pasture-<uid>` with the wrong owner or mode. If you own it, `chmod 700 /tmp/pasture-$(id -u)`; if you don't, remove it as root. It is refused on purpose. |
| Log: `connection refused ... uid N` | Another user tried to connect. Only your own user can. |
| Programs die when you log out | Linger is off: `loginctl enable-linger $USER`. Check with `loginctl show-user $USER -p Linger`. |
| Everything is gone after closing the WSL window | WSL shut the distribution down (2.3b). |
| goat says it can't reach the server | Check `systemctl --user is-active pasture` and that goat uses the same socket: `ls -l /tmp/pasture-$(id -u)/default`, and the same `PASTURE_TMPDIR`. |
| goat: `protocol version mismatch` | goat and pasture are from different builds. Upgrade and restart pasture (section 6). |
| A program runs in goat but not through pasture: `spawn: "x" not found in PATH` | pasture looks programs up in the PATH goat sends. Check goat's environment, or use absolute paths. |
| The service restarts in a loop | `journalctl --user -u pasture -p err -b` shows why. Restarts are unlimited (`StartLimitIntervalSec=0`), 2 s apart. |

For a deeper look:

```bash
systemctl --user show pasture -p ExecMainStatus -p NRestarts -p ActiveEnterTimestamp
journalctl --user -u pasture -o verbose -n 50
PASTURE_ARGS=-log-level=debug   # in pasture.env, then restart
```

---

## 9. The unit, line by line

```ini
[Unit]
Description=pasture mux server (keeps goat's programs running)
ConditionFileIsExecutable=%h/.local/bin/pasture   # skip (don't fail) when not installed
StartLimitIntervalSec=0                           # never give up restarting

[Service]
Type=simple                          # runs in the foreground; ready once started
Environment=PASTURE_ARGS=-log-level=info
EnvironmentFile=-%h/.config/pasture/pasture.env   # optional; overrides the default above
ExecStart=%h/.local/bin/pasture -log - $PASTURE_ARGS  # log to stderr -> journal
ExecReload=/bin/kill -HUP $MAINPID   # SIGHUP = reopen the log (harmless with the journal)
KillMode=mixed                       # SIGTERM to the server only; it hangs up its programs
KillSignal=SIGTERM
TimeoutStopSec=15                    # then SIGKILL to whatever is left
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target              # start with the user manager (login, or boot with linger)
```

`pasture -d` is not used here: under systemd the server runs in the
foreground, and systemd supervises it. `-d` is for starting it by hand
without systemd.
