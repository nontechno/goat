package main

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"syscall"
)

// Whether a background is dark or light: the text and frame colors that
// suit it depend on it. Window colors come from palettes (combos.go).

type tone int

const (
	toneDark tone = iota
	toneLight
)

func (t tone) String() string { return [...]string{"dark", "light"}[t] }

// toneOf guesses whether c is a dark or light color (perceived lightness).
func toneOf(c color.Color) tone {
	r, g, b, _ := c.RGBA()
	y := 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b) // 0..65535
	if y > 0.5*65535 {
		return toneLight
	}
	return toneDark
}

// readSmallFile reads a regular file of at most max bytes, without
// blocking on a FIFO or device put there.
func readSmallFile(path string, max int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("larger than %d bytes", max)
	}
	return data, nil
}
