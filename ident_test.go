package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestParseSSHArgs(t *testing.T) {
	for _, tt := range []struct {
		args       []string
		host, user string
		upTo       []string
	}{
		{[]string{"example.org"}, "example.org", "", []string{"example.org"}},
		{[]string{"bob@example.org", "uptime"}, "example.org", "bob", []string{"bob@example.org"}},
		{[]string{"-p", "2222", "-l", "bob", "box", "ls", "-l"}, "box", "bob", []string{"-p", "2222", "-l", "bob", "box"}},
		{[]string{"-lbob", "-4A", "-i", "~/.ssh/k", "box"}, "box", "bob", []string{"-lbob", "-4A", "-i", "~/.ssh/k", "box"}},
		{[]string{"-J", "jump@bastion", "alice@inner"}, "inner", "alice", []string{"-J", "jump@bastion", "alice@inner"}},
		{[]string{"-o", "User=x", "--", "h"}, "h", "", []string{"-o", "User=x", "--", "h"}},
		{[]string{"ssh://carol@db.example:2200"}, "db.example", "carol", []string{"ssh://carol@db.example:2200"}},
		{[]string{"ssh://[fe80::1]:22"}, "fe80::1", "", []string{"ssh://[fe80::1]:22"}},
		{[]string{"-V"}, "", "", nil},
	} {
		host, user, upTo := parseSSHArgs(tt.args)
		if host != tt.host || user != tt.user || !reflect.DeepEqual(upTo, tt.upTo) {
			t.Errorf("%q: got %q %q %q, want %q %q %q", tt.args, host, user, upTo, tt.host, tt.user, tt.upTo)
		}
	}
}

func TestIdentityLabel(t *testing.T) {
	id := identity{user: "bob", host: "box"}
	for _, tt := range []struct {
		u, h bool
		want string
	}{{true, true, "bob@box"}, {false, true, "box"}, {true, false, "bob"}, {false, false, ""}} {
		if got := id.label(tt.u, tt.h); got != tt.want {
			t.Errorf("label(%v,%v) = %q", tt.u, tt.h, got)
		}
	}
	if got := (identity{host: "box"}).label(true, true); got != "box" {
		t.Errorf("no user: %q", got)
	}
}

// A window whose foreground program is ssh shows where it logs in; when
// ssh ends, the window's own user and host come back.
func TestWindowIdentityFollowsSSH(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc")
	}
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("needs bash")
	}
	py, err := findPython()
	if err != nil {
		t.Skip("needs python3")
	}
	dir := t.TempDir()
	// A stand-in for ssh: "ssh -G" prints a config; anything else waits.
	fake := "#!/bin/sh\n[ \"$1\" = -G ] && { echo 'user alice'; echo 'hostname 10.1.2.3'; exit 0; }\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	// Window 1 runs a process named "ssh" (argv[0]); window 2 a plain
	// program. (exec: the program must lead the foreground process group,
	// as it does when an interactive shell starts it.)
	script := filepath.Join(dir, "run.sh")
	body := "exec -a ssh " + py + " -c 'import time; time.sleep(30)' -p 2222 box.example\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	m := newWM(cfg, nil, 80, 24)
	m.identOn, m.user, m.host = true, "me", "local-box"
	m.hostChecked = time.Now().Add(time.Hour) // keep "local-box"
	w1, err := newWindow(1, "/bin/bash "+script, 0, 0, 40, 10, winOpts{}, m.out)
	if err != nil {
		t.Fatal(err)
	}
	w2, err := newWindow(2, "sleep 30", 0, 0, 40, 10, winOpts{}, m.out)
	if err != nil {
		t.Fatal(err)
	}
	m.windows = []*Window{w1, w2}
	defer m.closeAll()

	wait := func(w *Window, want string) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			m.tick(time.Now())
			for drained := false; !drained; {
				select {
				case msg := <-m.identOut:
					m.handleIdent(msg)
				case <-m.out:
				default:
					drained = true
				}
			}
			if m.frameLabel(w) == want {
				return
			}
		}
		t.Fatalf("frame label %q, want %q", m.frameLabel(w), want)
	}
	wait(w1, "alice@box.example") // host as typed, user from "ssh -G"
	wait(w2, currentUserName()+"@local-box")
}

func findPython() (string, error) {
	for _, p := range []string{"/usr/bin/python3", "/usr/local/bin/python3", "/bin/python3"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}

// goat's own host label is re-read every few seconds.
func TestLocalHostRecheck(t *testing.T) {
	home := t.TempDir()
	m := newWM(DefaultConfig(), nil, 80, 24)
	m.home, m.host = home, "old"
	now := time.Now()
	m.hostChecked = now
	if m.refreshLocalIdent(now.Add(time.Second)) {
		t.Fatal("re-read before the interval")
	}
	if err := os.WriteFile(filepath.Join(home, ".hostname"), []byte("new-name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !m.refreshLocalIdent(now.Add(hostRecheckEvery)) || m.host != "new-name" {
		t.Fatalf("host %q after ~/.hostname changed", m.host)
	}
	if m.refreshLocalIdent(now.Add(3 * hostRecheckEvery)) {
		t.Fatal("reported a change without one")
	}
}
