package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/ansi"
)

// The color palette: background and text (foreground) colors, each dark or
// light, from four files in the config directory (installed by
// "goat -install"):
//
//	dark-background.color    backgrounds for a dark window
//	light-background.color   backgrounds for a light window
//	dark-foreground.color    text colors that read well on dark backgrounds
//	light-foreground.color   text colors that read well on light backgrounds
//
// Each is JSON:
//
//	{ "role": "background", "tone": "dark",
//	  "colors": { "Midnight": { "ansi": 17, "hex": "#00005f" }, ... } }
//
// The colors are kept in the order the file lists them. A color is used by
// its "ansi" palette number (like goat's other colors), else its "hex".
//
// Alt+b steps a window's background through all backgrounds, dark then
// light (a light one that is also in the dark list is left out), then back
// to the terminal's own; Alt+f steps its text color
// through the text colors for the tone of its background.

type tone int

const (
	toneDark tone = iota
	toneLight
)

func (t tone) String() string { return [...]string{"dark", "light"}[t] }

type namedColor struct {
	name string
	c    color.Color
	tone tone
}

type palette struct {
	bgs []namedColor    // all backgrounds, dark then light
	fgs [2][]namedColor // text colors by the tone they suit
}

// paletteFiles are the files, by role and tone.
var paletteFiles = []struct {
	name string
	role string
	tone tone
}{
	{"dark-background.color", "background", toneDark},
	{"light-background.color", "background", toneLight},
	{"dark-foreground.color", "foreground", toneDark},
	{"light-foreground.color", "foreground", toneLight},
}

// loadPalette reads the palette from dir. No files at all is not an error
// (nil palette: Alt+b and Alt+f say to run goat -install); a missing or
// broken file among the others is reported as a warning and left out.
func loadPalette(dir string) (*palette, []string) {
	p := &palette{}
	var warns []string
	found := 0
	for _, f := range paletteFiles {
		path := filepath.Join(dir, f.name)
		data, err := readSmallFile(path, 1<<20)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		found++
		var colors []namedColor
		if err == nil {
			colors, err = parseColorFile(data, f.role, f.tone)
		}
		if err != nil {
			warns = append(warns, fmt.Sprintf("%s: %v (ignored)", path, err))
			continue
		}
		if f.role == "background" {
			if f.tone == toneLight { // dark ones are already in: drop repeats
				colors = slices.DeleteFunc(colors, func(c namedColor) bool {
					dup := slices.ContainsFunc(p.bgs, func(d namedColor) bool { return sameColor(d.c, c.c) })
					if dup {
						logger.Info("light background already among the dark ones: left out", "file", path, "color", c.name)
					}
					return dup
				})
			}
			p.bgs = append(p.bgs, colors...)
		} else {
			p.fgs[f.tone] = append(p.fgs[f.tone], colors...)
		}
	}
	if found == 0 {
		return nil, nil
	}
	if found < len(paletteFiles) {
		warns = append(warns, fmt.Sprintf("%s: only %d of the 4 color files found (goat -install fetches them)", dir, found))
	}
	return p, warns
}

// parseColorFile reads one .color file, keeping the colors in file order.
func parseColorFile(data []byte, role string, t tone) ([]namedColor, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var out []namedColor
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		switch key {
		case "role", "tone":
			var v string
			if err := dec.Decode(&v); err != nil {
				return nil, fmt.Errorf("%q: %w", key, err)
			}
			want := role
			if key == "tone" {
				want = t.String()
			}
			if !strings.EqualFold(v, want) {
				return nil, fmt.Errorf("%q is %q, expected %q for this file", key, v, want)
			}
		case "colors":
			if out, err = parseColorList(dec, t); err != nil {
				return nil, err
			}
		default: // something newer: skip it
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no colors")
	}
	return out, nil
}

// parseColorList reads the "colors" object: name -> {ansi, hex}, in order.
func parseColorList(dec *json.Decoder, t tone) ([]namedColor, error) {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New(`"colors" is not an object`)
	}
	var out []namedColor
	for dec.More() {
		nameTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, _ := nameTok.(string)
		var v struct {
			ANSI *int   `json:"ansi"`
			Hex  string `json:"hex"`
		}
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("color %q: %w", name, err)
		}
		var c color.Color
		switch {
		case v.ANSI != nil && *v.ANSI >= 0 && *v.ANSI <= 255:
			c = ansi.IndexedColor(*v.ANSI)
		case v.Hex != "":
			var err error
			if c, err = parseHexColor(v.Hex); err != nil {
				return nil, fmt.Errorf("color %q: %w", name, err)
			}
		default:
			return nil, fmt.Errorf("color %q: needs \"ansi\" (0-255) or \"hex\"", name)
		}
		out = append(out, namedColor{name: name, c: c, tone: t})
	}
	if _, err := dec.Token(); err != nil { // closing }
		return nil, err
	}
	return out, nil
}

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
