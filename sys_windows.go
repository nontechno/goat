//go:build windows

package main

import (
	"os"
	"time"
)

// goat needs a Unix terminal and pseudo-terminals: it doesn't run on
// Windows itself (use it in WSL, or over ssh). These stand-ins only let it
// build and be checked there (an editor on Windows, go vet).

func resizeSignals() []os.Signal { return nil }

func processCPUTime() (time.Duration, bool) { return 0, false }

func hangUpGroup(int) {}

func canEnter(string) bool { return true }

func pollable(f *os.File) *os.File { return f }

func setWinsize(*os.File, int, int) {}

func unsupportedPlatform() string {
	return "goat runs in a Unix terminal (Linux, macOS, the BSDs): on Windows, run it in WSL or over ssh"
}
