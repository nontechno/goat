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
