package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"

	uv "github.com/charmbracelet/ultraviolet"
)

// maxHostLabel is the longest name taken from ~/.hostname, in characters.
const maxHostLabel = 32

// hostLabel is the host name shown in window frames (show_host): the first
// line of ~/.hostname, trimmed and cut to 32 characters, if there is one;
// otherwise the system's host name. source says which ("file" or
// "system"; "" if neither gave a name).
func hostLabel(home string) (name, source string) {
	if home != "" {
		if n := readHostFile(filepath.Join(home, ".hostname")); n != "" {
			return n, "file"
		}
	}
	if n, err := os.Hostname(); err == nil {
		if n = cleanHostName(n); n != "" {
			return n, "system"
		}
	}
	return "", ""
}

// readHostFile returns the cleaned first line of path ("" if the file is
// missing, unreadable or its first line is blank).
func readHostFile(path string) string {
	// Only a regular file, opened without blocking: a FIFO (or a device)
	// there would otherwise stop goat's event loop on open or read.
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return "" // replaced between the two checks
	}
	// Read at most 4 KB: the first line is all that's wanted, and a huge or
	// odd file (a device, a binary) mustn't stall startup.
	sc := bufio.NewScanner(bufio.NewReaderSize(f, 4096))
	sc.Buffer(make([]byte, 4096), 4096)
	if !sc.Scan() {
		return ""
	}
	return cleanHostName(sc.Text())
}

// cleanHostName trims spaces, drops control and other invisible characters
// (so the frame can't be broken by escape sequences), and cuts the name to
// maxHostLabel characters.
func cleanHostName(s string) string {
	s = strings.Map(func(r rune) rune {
		if !unicode.IsPrint(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxHostLabel {
		s = strings.TrimSpace(string(r[:maxHostLabel]))
	}
	return s
}

// drawHost writes the host name into the left of the bottom border:
// └─ myhost ─────── wrap ─┘. When the window is too narrow for all of it,
// the name is shortened (keeping at least 4 columns) or left out; it never
// covers the wrap indicator.
func (m *WM) drawHost(scr uv.Screen, w *Window, st uv.Style) {
	if !m.showIdent() || w.background || w.frame == frameNone {
		return
	}
	name := m.frameLabel(w)
	if name == "" {
		return
	}
	start := w.x + 2       // └─␠host
	limit := w.x + w.w - 2 // stop before ─┘
	label := " " + wrapLabel(w) + " "
	if wx := w.x + w.w - 2 - len(label); wx >= w.x+2 {
		limit = wx - 1 // keep one ─ between the host and the wrap label
	}
	maxW := limit - start - 2 // the spaces around the name
	if maxW < 1 {
		return
	}
	cs := textCells(name, st, maxW)
	// A name cut to a few characters says nothing: show at least 4 columns
	// (or the whole name, when it is shorter), or nothing.
	if w := cellsWidth(cs); w == 0 || (w < 4 && w < cellsWidth(textCells(name, st, 1<<20))) {
		return
	}
	y := w.y + w.h - 1
	put(scr, start, y, runeCell(' ', st))
	end := putCells(scr, start+1, y, cs)
	put(scr, end, y, runeCell(' ', st))
}
