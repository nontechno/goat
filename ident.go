package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// A window's identity is the user and host its program runs as, shown on
// the bottom border (show_user, show_host):
//
//   - normally goat's host (~/.hostname or the system name) and the user of
//     the window's foreground process, so "su" or "sudo -i" shows root;
//   - while the foreground process is ssh, the user and host it connects to:
//     the host as typed on the command line, the user as ssh will use it
//     (from "ssh -G", which applies ~/.ssh/config without connecting).
//
// It is polled on goat's existing twice-a-second tick, only while shown:
// per window, one ioctl for the foreground process group and a read of
// /proc/<pid>/status; "ssh -G" runs once per ssh process, in the
// background. Linux only (/proc); elsewhere the identity is goat's own.
//
// Not visible this way: what happens inside the ssh session (su, sudo, a
// further ssh), since that runs on the other machine.

type identity struct {
	user, host string
}

// label is what the frame shows.
func (id identity) label(showUser, showHost bool) string {
	switch {
	case showUser && showHost && id.user != "" && id.host != "":
		return id.user + "@" + id.host
	case showHost && id.host != "":
		return id.host
	case showUser && id.user != "":
		return id.user
	}
	return ""
}

// identMsg carries the result of "ssh -G" back to the event loop.
type identMsg struct {
	w    *Window
	pid  int // the ssh process it was resolved for
	user string
	host string
}

const (
	sshResolveTimeout = 3 * time.Second
	hostRecheckEvery  = 5 * time.Second
)

// showIdent reports whether identities are shown (and so polled).
func (m *WM) showIdent() bool { return m.cfg.ShowHost || m.cfg.ShowUser }

// localIdent is goat's own identity.
func (m *WM) localIdent() identity { return identity{user: m.user, host: m.host} }

// frameLabel is the identity text for w's bottom border.
func (m *WM) frameLabel(w *Window) string {
	id := w.ident
	if id == (identity{}) {
		id = m.localIdent()
	}
	return id.label(m.cfg.ShowUser, m.cfg.ShowHost)
}

// refreshIdent updates w's identity from its foreground process; reports a
// change.
func (m *WM) refreshIdent(w *Window) bool {
	if w.closed || w.cmd == nil || w.ptmx == nil {
		return false
	}
	id := m.localIdent()
	pid := foregroundPid(w.ptmx, w.cmd)
	if pid > 0 {
		args := processArgs(pid)
		if len(args) > 0 && filepath.Base(args[0]) == "ssh" {
			if pid != w.sshPid {
				m.startSSHIdent(w, pid, args[1:])
			}
			id = w.sshIdent
		} else {
			w.sshPid, w.sshIdent = 0, identity{}
			if uid, ok := processEUID(pid); ok {
				if name := m.userName(uid); name != "" {
					id.user = name
				}
			}
		}
	}
	if id == w.ident {
		return false
	}
	logger.Info("window identity changed", "window", w.id, "from", w.ident.label(true, true),
		"to", id.label(true, true), "pid", pid)
	w.ident = id
	return true
}

// startSSHIdent shows what the ssh command line says right away, and asks
// "ssh -G" (in the background) for the user ssh will really log in as.
func (m *WM) startSSHIdent(w *Window, pid int, args []string) {
	dest, user, upTo := parseSSHArgs(args)
	w.sshPid = pid
	w.sshIdent = identity{user: user, host: dest}
	if dest == "" {
		logger.Debug("ssh without a destination; identity unknown", "window", w.id, "pid", pid)
		w.sshIdent = m.localIdent()
		return
	}
	if w.sshIdent.user == "" {
		w.sshIdent.user = m.user // until ssh -G says otherwise (usually the same)
	}
	out := m.identOut
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sshResolveTimeout)
		defer cancel()
		start := time.Now()
		u, h, err := sshConfig(ctx, upTo)
		if err != nil {
			logger.Debug("ssh -G failed; keeping the command line's user", "pid", pid, "err", err)
			return
		}
		logger.Debug("ssh -G", "pid", pid, "user", u, "hostname", h, "took", time.Since(start).Round(time.Millisecond))
		out <- identMsg{w: w, pid: pid, user: u, host: h}
	}()
}

// handleIdent applies an "ssh -G" result, if w still runs that ssh.
func (m *WM) handleIdent(msg identMsg) {
	w := msg.w
	if w.closed || w.sshPid != msg.pid {
		return
	}
	if msg.user != "" {
		w.sshIdent.user = msg.user
	}
	if w.sshIdent.host == "" {
		w.sshIdent.host = msg.host
	}
	if m.refreshIdent(w) {
		m.dirty = true
	}
}

// refreshLocalIdent re-reads goat's host label (~/.hostname or the system
// name) every hostRecheckEvery; reports a change.
func (m *WM) refreshLocalIdent(now time.Time) bool {
	if now.Sub(m.hostChecked) < hostRecheckEvery {
		return false
	}
	m.hostChecked = now
	home := m.home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	h, source := hostLabel(home)
	if h == m.host {
		return false
	}
	logger.Info("host name changed", "from", m.host, "to", h, "source", source)
	m.host = h
	return true
}

// userName is the login name for uid (cached; "" if unknown).
func (m *WM) userName(uid int) string {
	if n, ok := m.userNames[uid]; ok {
		return n
	}
	n := ""
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		n = u.Username
	} else {
		n = strconv.Itoa(uid)
	}
	if m.userNames == nil {
		m.userNames = map[int]string{}
	}
	m.userNames[uid] = n
	return n
}

// currentUserName is goat's own login name.
func currentUserName() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return strconv.Itoa(os.Getuid())
}

// sshOptsWithArg are ssh's options that take an argument (OpenSSH).
const sshOptsWithArg = "BbcDEeFIiJLlmOopQRSWw"

// parseSSHArgs finds the destination in ssh's arguments. It returns the
// host as typed, the user given with user@host or -l (else ""), and the
// arguments up to and including the destination (for "ssh -G").
func parseSSHArgs(args []string) (host, user string, upTo []string) {
	loginFlag := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			if i+1 < len(args) {
				host, user = splitSSHDest(args[i+1])
				upTo = args[:i+2]
			}
			break
		}
		if len(a) > 1 && a[0] == '-' {
			for j := 1; j < len(a); j++ {
				if strings.IndexByte(sshOptsWithArg, a[j]) < 0 {
					continue
				}
				val := a[j+1:]
				if val == "" && i+1 < len(args) {
					i++
					val = args[i]
				}
				if a[j] == 'l' {
					loginFlag = val
				}
				break
			}
			continue
		}
		host, user = splitSSHDest(a)
		upTo = args[:i+1]
		break
	}
	if user == "" {
		user = loginFlag
	}
	return cleanHostName(host), cleanHostName(user), upTo
}

// splitSSHDest splits "[user@]host" or "ssh://[user@]host[:port]".
func splitSSHDest(d string) (host, user string) {
	if rest, ok := strings.CutPrefix(d, "ssh://"); ok {
		d = rest
		if i := strings.IndexByte(d, '/'); i >= 0 {
			d = d[:i]
		}
		if i := strings.LastIndexByte(d, '@'); i >= 0 {
			user, d = d[:i], d[i+1:]
		}
		if strings.HasPrefix(d, "[") {
			if i := strings.IndexByte(d, ']'); i > 0 {
				return d[1:i], user
			}
		}
		if i := strings.LastIndexByte(d, ':'); i >= 0 && strings.Count(d, ":") == 1 {
			d = d[:i]
		}
		return d, user
	}
	if i := strings.LastIndexByte(d, '@'); i >= 0 {
		user, d = d[:i], d[i+1:]
	}
	return d, user
}

// sshConfig asks ssh for the user and host name it would use for these
// arguments ("ssh -G" prints the effective configuration and exits
// without connecting).
func sshConfig(ctx context.Context, args []string) (user, host string, err error) {
	cmd := exec.CommandContext(ctx, "ssh", append([]string{"-G"}, args...)...)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return "", "", err
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), " ")
		switch k {
		case "user":
			user = cleanHostName(v)
		case "hostname":
			host = cleanHostName(v)
		}
	}
	return user, host, nil
}
