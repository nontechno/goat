package main

import (
	"fmt"
	"strconv"
	"strings"
)

// Settings for this machine from ~/.config/goat/local.toml that change how
// goat behaves (host.go localConfig has them all): wrap_lines (applied to
// the config at start, see main), new_window_size, confirm_quit,
// exit_on_last_window. Read once, at start.

// localOpts are the local.toml settings goat keeps.
type localOpts struct {
	newSize          windowSize // zero: config.toml's new_window_*_ratio
	confirmQuit      bool       // ask before quitting with floating windows open
	exitOnLastWindow bool       // quit when the last floating window closes
}

// defaultLocalOpts is goat's behavior without local.toml.
func defaultLocalOpts() localOpts { return localOpts{confirmQuit: true} }

// windowSize is a new window's size: each side in columns/rows, or a share
// of the screen (pct > 0).
type windowSize struct {
	cols, rows       int
	colsPct, rowsPct float64 // 0 < pct <= 1
}

func (s windowSize) set() bool {
	return s.cols > 0 || s.rows > 0 || s.colsPct > 0 || s.rowsPct > 0
}

// parseWindowSize reads "100x30", "60%x50%" or a mix ("100x50%").
func parseWindowSize(v string) (windowSize, error) {
	var s windowSize
	c, r, ok := strings.Cut(strings.ToLower(strings.ReplaceAll(v, " ", "")), "x")
	if !ok {
		return s, fmt.Errorf("want COLUMNSxROWS (100x30) or shares of the screen (60%%x50%%)")
	}
	side := func(name, part string, n *int, pct *float64) error {
		if part == "" {
			return fmt.Errorf("%s missing: want COLUMNSxROWS (100x30) or shares (60%%x50%%)", name)
		}
		if p, isPct := strings.CutSuffix(part, "%"); isPct {
			f, err := strconv.ParseFloat(p, 64)
			if err != nil || f <= 0 || f > 100 {
				return fmt.Errorf("%q: want a share from 1%% to 100%%", part)
			}
			*pct = f / 100
			return nil
		}
		i, err := strconv.Atoi(part)
		if err != nil || i < 1 {
			return fmt.Errorf("%q: want a number of columns/rows, or a share like 50%%", part)
		}
		*n = i
		return nil
	}
	if err := side("columns", c, &s.cols, &s.colsPct); err != nil {
		return windowSize{}, err
	}
	if err := side("rows", r, &s.rows, &s.rowsPct); err != nil {
		return windowSize{}, err
	}
	return s, nil
}

// readLocalOpts turns local.toml's settings into localOpts; problems come
// back as warnings (the setting is then ignored).
func readLocalOpts(lc localConfig) (localOpts, []string) {
	o := defaultLocalOpts()
	var warns []string
	if v := strings.TrimSpace(lc.NewWindowSize); v != "" {
		if s, err := parseWindowSize(v); err != nil {
			warns = append(warns, fmt.Sprintf("local.toml: new_window_size %q: %v", v, err))
		} else {
			o.newSize = s
		}
	}
	if lc.ConfirmQuit != nil {
		o.confirmQuit = *lc.ConfirmQuit
	}
	if lc.ExitOnLastWindow != nil {
		o.exitOnLastWindow = *lc.ExitOnLastWindow
	}
	return o, warns
}

// newWindowSize is the size of a new window without one of its own: from
// local.toml's new_window_size, else config.toml's ratios; within minW..cols
// and minH..area either way.
func (m *WM) newWindowSize(minH, area int) (w, h int) {
	l := m.cfg.Layout
	w = int(float64(m.cols) * l.NewWindowWidthRatio)
	h = int(float64(area) * l.NewWindowHeightRatio)
	s := m.local.newSize
	switch {
	case s.cols > 0:
		w = s.cols
	case s.colsPct > 0:
		w = int(float64(m.cols) * s.colsPct)
	}
	switch {
	case s.rows > 0:
		h = s.rows
	case s.rowsPct > 0:
		h = int(float64(area) * s.rowsPct)
	}
	return clamp(w, m.minW(), max(m.cols, m.minW())), clamp(h, minH, max(area, minH))
}
