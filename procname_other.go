//go:build !linux

package main

import (
	"os"
	"os/exec"
)

// foregroundProcessName and foregroundDir are only implemented on Linux (via
// /proc); elsewhere titles fall back to the shell name or the program-set
// title, and directories to what the shell reports (OSC 7).
func foregroundProcessName(*os.File, *exec.Cmd) string { return "" }
func foregroundDir(*os.File, *exec.Cmd) string         { return "" }

// Without /proc the window identity (show_user/show_host) is goat's own
// user and host.
func foregroundPid(*os.File, *exec.Cmd) int { return 0 }
func processEUID(int) (int, bool)           { return 0, false }
func processUIDs(int) (int, int, bool)      { return 0, 0, false }
func processArgs(int) []string              { return nil }
