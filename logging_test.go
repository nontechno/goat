package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withLog points the log at a temporary file for one test.
func withLog(t *testing.T, cfg *Config) string {
	t.Helper()
	old, oldPath := logger, logPath
	t.Cleanup(func() { logger, logPath = old, oldPath })
	path := filepath.Join(t.TempDir(), "sub", "goat.log")
	cfg.LogFile = path
	warn, closeLog := setupLog(cfg)
	t.Cleanup(closeLog)
	if warn != "" {
		t.Fatal(warn)
	}
	return path
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLogLevelsAndFile(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LogLevel = "warn"
	path := withLog(t, cfg)
	logger.Info("hidden at warn level")
	logger.Warn("shown", "k", "v")
	if logPath != path {
		t.Fatalf("logPath %q", logPath)
	}
	got := readLog(t, path)
	if strings.Contains(got, "hidden") || !strings.Contains(got, "msg=shown") || !strings.Contains(got, "k=v") || !strings.Contains(got, "pid=") {
		t.Fatalf("log:\n%s", got)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("log mode %v", fi.Mode().Perm())
	}
}

func TestLogOffAndBadPath(t *testing.T) {
	old, oldPath := logger, logPath
	defer func() { logger, logPath = old, oldPath }()
	cfg := DefaultConfig()
	cfg.LogFile = "none"
	if w, _ := setupLog(cfg); w != "" || logPath != oldPath {
		t.Fatalf("none: warning %q, path %q", w, logPath)
	}
	blocker := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(blocker, nil, 0o600)
	cfg.LogFile = filepath.Join(blocker, "goat.log") // a directory that is a file
	if w, _ := setupLog(cfg); !strings.Contains(w, "not logging") {
		t.Fatalf("bad path: warning %q", w)
	}
	if c, warns := parseConfig(`log_level = "loud"`); c.LogLevel != "info" || len(warns) != 1 {
		t.Fatalf("bad level: %q %v", c.LogLevel, warns)
	}
}

func TestDefaultLogPath(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/x/state")
	if p := defaultLogPath(); p != "/x/state/goat/goat.log" {
		t.Fatalf("%q", p)
	}
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/sp")
	if p := defaultLogPath(); p != "/home/sp/.local/state/goat/goat.log" {
		t.Fatalf("%q", p)
	}
	if p := expandHome("~/logs/g.log"); p != "/home/sp/logs/g.log" {
		t.Fatalf("%q", p)
	}
}

func TestCrashReport(t *testing.T) {
	path := withLog(t, DefaultConfig())
	m, _, _, _ := wmWithBackground(t)
	old := crashState
	defer func() { crashState = old }()
	crashState = m.snapshot
	func() {
		defer func() {
			if r := recover(); r != nil {
				reportCrash(r)
			}
		}()
		panic(errors.New("boom"))
	}()
	got := readLog(t, path)
	for _, want := range []string{"level=ERROR", "msg=crash", "panic=boom", "screen=40x12", "win1 at", "TestCrashReport"} {
		if !strings.Contains(got, want) {
			t.Fatalf("crash report lacks %q:\n%s", want, got)
		}
	}
	// A broken state snapshot doesn't lose the report.
	crashState = func() string { panic("worse") }
	reportCrash("again")
	if got := readLog(t, path); !strings.Contains(got, "state unavailable: worse") {
		t.Fatalf("log:\n%s", got)
	}
}

func TestExcerpt(t *testing.T) {
	if got := excerpt([]byte("ab\x1b[1mc"), 100); got != `"ab\x1b[1mc"` {
		t.Fatalf("%s", got)
	}
	if got := excerpt([]byte("0123456789"), 4); got != `…(6 bytes before) "6789"` {
		t.Fatalf("%s", got)
	}
}
