//go:build unix

package pasture

import (
	"fmt"
	"os"
	"path/filepath"
)

// SocketDir is this user's socket directory: $PASTURE_TMPDIR, else /tmp,
// plus "pasture-<uid>". It is created with mode 0700, and refused if it has
// another owner or looser permissions.
//
// $TMPDIR is deliberately not used (as in tmux): it differs between a
// service manager, a desktop terminal and an SSH login on the same machine
// (notably on macOS), and they must all find the same socket.
func SocketDir() (string, error) {
	base := os.Getenv("PASTURE_TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	dir := filepath.Join(base, fmt.Sprintf("pasture-%d", os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := checkPrivateDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// DefaultSocket is the socket path used when none is given:
// <SocketDir>/default.
func DefaultSocket() (string, error) {
	dir, err := SocketDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "default"), nil
}
