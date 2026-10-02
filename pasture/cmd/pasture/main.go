//go:build unix

// Command pasture is the mux server: it keeps programs running on
// pseudo-terminals for goat (see package github.com/nontechno/goat/pasture).
//
//	pasture [-S socket] [-log file|-] [-log-level debug|info|warn|error] [-d]
//
// It runs in the foreground (for systemd, launchd or a terminal) unless -d
// is given, in which case it detaches into the background and exits once
// the server is accepting connections.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nontechno/goat/pasture"
)

const daemonEnv = "PASTURE_DAEMON_CHILD"

func main() {
	os.Exit(run())
}

func run() int {
	sock := flag.String("S", "", "socket path (default: $PASTURE_TMPDIR or /tmp, + /pasture-<uid>/default)")
	logPath := flag.String("log", "", `log file; "-" = stderr (default: stderr, or <socket dir>/pasture.log with -d)`)
	level := flag.String("log-level", "info", "debug, info, warn or error")
	daemon := flag.Bool("d", false, "run in the background")
	version := flag.Bool("version", false, "print the protocol version and exit")
	histDir := flag.String("history-dir", "", "directory for history moved out of memory (default: <socket>.history)")
	histMax := flag.String("history-max", "64M", "history kept on disk per pane (K, M, G suffixes; 0 = no history on disk)")
	flag.Parse()
	if *version {
		fmt.Printf("pasture protocol %d\n", pasture.ProtocolVersion)
		return 0
	}
	if flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "pasture: unexpected arguments:", flag.Args())
		return 2
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(*level)); err != nil {
		fmt.Fprintln(os.Stderr, "pasture: bad -log-level:", *level)
		return 2
	}
	if *sock == "" {
		p, err := pasture.DefaultSocket()
		if err != nil {
			fmt.Fprintln(os.Stderr, "pasture:", err)
			return 1
		}
		*sock = p
	}
	abs, err := filepath.Abs(*sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pasture:", err)
		return 1
	}
	*sock = abs
	inChild := os.Getenv(daemonEnv) == "1"
	if *logPath == "" && (*daemon || inChild) {
		*logPath = filepath.Join(filepath.Dir(*sock), "pasture.log")
	}
	if *daemon {
		return startDaemon(*sock, *logPath)
	}
	os.Unsetenv(daemonEnv)

	out, err := openLog(*logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pasture: log:", err)
		return 1
	}
	log := slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: lvl}))
	// SIGHUP reopens the log file (for logrotate).
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			if err := out.reopen(); err != nil {
				log.Error("reopening log failed", "err", err)
			} else {
				log.Info("log reopened")
			}
		}
	}()

	maxBytes, err := parseBytes(*histMax)
	if err != nil {
		log.Error("bad -history-max", "value", *histMax, "err", err)
		return 2
	}
	if *histDir == "" {
		*histDir = *sock + ".history"
	}
	srv, err := pasture.NewServer(pasture.Config{Socket: *sock, Logger: log,
		History: pasture.HistoryConfig{Dir: *histDir, MaxBytes: maxBytes}})
	if err != nil {
		log.Error("cannot start", "err", err)
		if out.path != "" {
			fmt.Fprintln(os.Stderr, "pasture:", err)
		}
		return 1
	}
	if err := srv.Serve(); err != nil {
		log.Error("server failed", "err", err)
		return 1
	}
	return 0
}

// startDaemon runs this program again without -d, in a new session (no
// controlling terminal, so no SIGHUP when the terminal goes away), with
// stdio on /dev/null and / as its directory. It returns once the server
// accepts connections, or reports why it didn't start.
func startDaemon(sock, logPath string) int {
	// Already running: nothing to do (so "pasture -d" can be used as an
	// idempotent "make sure it runs").
	if c, err := net.Dial("unix", sock); err == nil {
		c.Close()
		pid, _ := os.ReadFile(sock + ".lock")
		fmt.Printf("pasture: already running, pid %s, socket %s\n", strings.TrimSpace(string(pid)), sock)
		return 0
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "pasture:", err)
		return 1
	}
	args := []string{"-S", sock, "-log", logPath}
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "log-level", "history-dir", "history-max":
			args = append(args, "-"+f.Name, f.Value.String())
		}
	})
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), daemonEnv+"=1")
	cmd.Dir = "/"
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil { // stdio nil = /dev/null
		fmt.Fprintln(os.Stderr, "pasture:", err)
		return 1
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case err := <-exited:
			fmt.Fprintf(os.Stderr, "pasture: server exited during start (%v); see %s\n", err, logPath)
			return 1
		case <-deadline:
			fmt.Fprintf(os.Stderr, "pasture: server not accepting connections after 5s; see %s\n", logPath)
			return 1
		case <-time.After(20 * time.Millisecond):
			if c, err := net.Dial("unix", sock); err == nil {
				c.Close() // no hello: the server logs and drops it
				fmt.Printf("pasture: pid %d, socket %s, log %s\n", cmd.Process.Pid, sock, logPath)
				return 0
			}
		}
	}
}

// logFile is the log destination, reopenable on SIGHUP.
type logFile struct {
	path string // "" = stderr
	mu   sync.Mutex
	f    *os.File
}

func openLog(path string) (*logFile, error) {
	if path == "-" {
		path = ""
	}
	l := &logFile{path: path}
	return l, l.reopen()
}

func (l *logFile) reopen() error {
	if l.path == "" {
		l.mu.Lock()
		l.f = os.Stderr
		l.mu.Unlock()
		return nil
	}
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	l.mu.Lock()
	old := l.f
	l.f = f
	l.mu.Unlock()
	if old != nil && old != os.Stderr {
		old.Close()
	}
	return nil
}

func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Write(p)
}

// parseBytes reads a size like "64M", "1G", "512K" or "1048576".
func parseBytes(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "K"):
		mult, s = 1<<10, strings.TrimSuffix(s, "K")
	case strings.HasSuffix(s, "M"):
		mult, s = 1<<20, strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "G"):
		mult, s = 1<<30, strings.TrimSuffix(s, "G")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("want a size like 64M")
	}
	return n * mult, nil
}
