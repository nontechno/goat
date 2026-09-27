//go:build !linux

package main

import (
	"os"
	"os/exec"
)

// foregroundProcessName is only implemented on Linux (via /proc); elsewhere
// titles fall back to the shell name or the program-set title.
func foregroundProcessName(*os.File, *exec.Cmd) string { return "" }
