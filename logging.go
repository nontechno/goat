package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
)

// Logging of problems, for diagnosing them afterwards.
//
// The log (log_file; by default $XDG_STATE_HOME/goat/goat.log, i.e.
// ~/.local/state/goat/goat.log) records, at the configured log_level:
//
//	error  crashes (with stack and a snapshot of goat's state), a terminal
//	       emulator failing on some output (with that output), losing the
//	       host terminal
//	warn   config problems, windows that can't start, clipboard commands
//	       failing, programs that can't be written to, ...
//	info   the start and end of a session (version, OS, terminal, sizes),
//	       windows opening and closing with their exit status
//	debug  escape sequences the terminal emulator doesn't handle
//
// Crashes that can't be recovered (a panic in another goroutine, a fatal
// runtime error) are written there too, by the Go runtime.

// logger is goat's log; it discards everything until setupLog.
var logger = slog.New(discardHandler{})

// logPath is where the log is written ("" when logging is off).
var logPath string

const logMaxSize = 4 << 20 // rotate to goat.log.1 beyond this at startup

type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (d discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discardHandler) WithGroup(string) slog.Handler           { return d }

// defaultLogPath is $XDG_STATE_HOME/goat/goat.log or
// ~/.local/state/goat/goat.log.
func defaultLogPath() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "goat", "goat.log")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "state", "goat", "goat.log")
	}
	return ""
}

// expandHome turns a leading ~/ into the home directory.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}

func parseLogLevel(s string) (slog.Level, bool) {
	switch strings.ToLower(s) {
	case "error":
		return slog.LevelError, true
	case "warn", "warning":
		return slog.LevelWarn, true
	case "info", "":
		return slog.LevelInfo, true
	case "debug":
		return slog.LevelDebug, true
	}
	return slog.LevelInfo, false
}

// setupLog opens the log per the config. It never fails goat: a log that
// can't be opened is reported (returned as a warning) and logging stays off.
// The returned function closes the log.
func setupLog(cfg *Config) (warning string, closeLog func()) {
	closeLog = func() {}
	path := cfg.LogFile
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "none", "off":
		return "", closeLog
	case "":
		path = defaultLogPath()
	}
	path = expandHome(path)
	if path == "" {
		return "", closeLog
	}
	level, _ := parseLogLevel(cfg.LogLevel)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Sprintf("log_file: %v (not logging)", err), closeLog
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > logMaxSize {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Sprintf("log_file: %v (not logging)", err), closeLog
	}
	logPath = path
	logger = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: level})).
		With("pid", os.Getpid())
	// Crashes goat can't recover from (a panic in another goroutine, a
	// fatal runtime error) go to the log too, with every goroutine's stack.
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
	return "", func() { _ = f.Close() }
}

// logSessionStart records what is needed to make sense of later entries.
func logSessionStart(cfgPath string, cols, rows int) {
	bi, _ := debug.ReadBuildInfo()
	goVer := runtime.Version()
	if bi != nil {
		goVer = bi.GoVersion
	}
	logger.Info("start",
		"version", version, "go", goVer, "os", runtime.GOOS, "arch", runtime.GOARCH,
		"term", os.Getenv("TERM"), "term_program", os.Getenv("TERM_PROGRAM"),
		"lc_terminal", os.Getenv("LC_TERMINAL"), "ssh", os.Getenv("SSH_CONNECTION") != "",
		"tmux", os.Getenv("TMUX") != "", "config", cfgPath, "size", fmt.Sprintf("%dx%d", cols, rows))
}

// excerpt shows (the end of) program output safely in a log line: printable
// text as is, everything else escaped, at most max bytes (the last ones:
// those are what a failure happened on).
func excerpt(b []byte, max int) string {
	cut := ""
	if len(b) > max {
		b, cut = b[len(b)-max:], fmt.Sprintf("…(%d bytes before) ", len(b)-max)
	}
	q := fmt.Sprintf("%q", b)
	return cut + q
}

// vtLogger passes the emulator's own messages (unhandled sequences, ...)
// to the log at debug level.
type vtLogger struct{ win int }

func (l vtLogger) Printf(format string, v ...any) {
	if !logger.Enabled(context.Background(), slog.LevelDebug) {
		return // don't format what won't be written
	}
	logger.Debug("emulator", "window", l.win, "msg", fmt.Sprintf(format, v...))
}
