package main

import (
	"errors"
	"fmt"
	"image/color"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/x/ansi"
)

// Color is a config color: an ANSI palette index (0-255), "#rrggbb", or
// "default" for the terminal's own color (C == nil).
type Color struct{ C color.Color }

// UnmarshalTOML accepts an integer palette index, "#rrggbb" or "default".
func (c *Color) UnmarshalTOML(v any) error {
	switch v := v.(type) {
	case int64:
		if v < 0 || v > 255 {
			return fmt.Errorf("color index %d out of range 0-255", v)
		}
		c.C = ansi.IndexedColor(uint8(v))
		return nil
	case string:
		col, err := parseColorString(v)
		if err != nil {
			return err
		}
		c.C = col
		return nil
	}
	return fmt.Errorf("color must be 0-255, \"#rrggbb\" or \"default\", got %v", v)
}

// parseColorString parses "default", "#rrggbb" or a palette index "0".."255".
func parseColorString(s string) (color.Color, error) {
	s = strings.TrimSpace(s)
	if s == "default" || s == "" {
		return nil, nil
	}
	if strings.HasPrefix(s, "#") {
		return parseHexColor(s)
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 255 {
		return nil, fmt.Errorf("color %q: want 0-255, \"#rrggbb\" or \"default\"", s)
	}
	return ansi.IndexedColor(uint8(n)), nil
}

func parseHexColor(s string) (color.Color, error) {
	h := strings.TrimPrefix(s, "#")
	if len(h) != 6 || h == s {
		return nil, fmt.Errorf("color %q: want \"#rrggbb\"", s)
	}
	n, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return nil, fmt.Errorf("color %q: %w", s, err)
	}
	return color.RGBA{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n), A: 0xff}, nil
}

func idx(n uint8) Color { return Color{C: ansi.IndexedColor(n)} }

// ThemeConfig holds UI colors.
type ThemeConfig struct {
	FocusedBorder   Color  `toml:"focused_border"`
	UnfocusedBorder Color  `toml:"unfocused_border"`
	PinnedBorder    Color  `toml:"pinned_border"`
	HintText        Color  `toml:"hint_text"` // status bar messages
	StatusFg        Color  `toml:"status_fg"`
	StatusBg        Color  `toml:"status_bg"`
	ClockFg         Color  `toml:"clock_fg"` // the clock at the right of the status bar
	ClockBg         Color  `toml:"clock_bg"`
	CompactTitleBg  *Color `toml:"compact_title_bg"` // compact border bg; nil = terminal default

	// New windows (Alt+c) take the palette after the focused window's (the
	// next background of its theme), so they don't look like the window
	// they came from; when false, the focused window's colors.
	NewWindowNextColors bool `toml:"new_window_next_colors"`
	// Background / text color pairs below this contrast (1-21; WCAG ratio)
	// aren't used: a launch script's bg / fg that would be unreadable gets
	// a readable text color. 0 = off.
	MinTextContrast float64 `toml:"min_text_contrast"`
}

// KeyConfig holds Alt+<key> bindings. Each value is a single character.
type KeyConfig struct {
	NewWindow   string `toml:"new_window"`
	FocusNext   string `toml:"focus_next"`
	FocusPrev   string `toml:"focus_prev"`
	Quit        string `toml:"quit"`
	CloseWindow string `toml:"close_window"`
	MoveLeft    string `toml:"move_left"`
	MoveDown    string `toml:"move_down"`
	MoveUp      string `toml:"move_up"`
	MoveRight   string `toml:"move_right"`
	ResizeLeft  string `toml:"resize_left"`
	ResizeDown  string `toml:"resize_down"`
	ResizeUp    string `toml:"resize_up"`
	ResizeRight string `toml:"resize_right"`
	PinWindow   string `toml:"pin_window"`

	Palette        string `toml:"palette"`     // choose the active window's colors (paldlg.go)
	ToggleWrap     string `toml:"toggle_wrap"` // wrap long lines or not
	ScrollLeft     string `toml:"scroll_left"` // view sideways (no-wrap)
	ScrollRight    string `toml:"scroll_right"`
	ShowEvents     string `toml:"show_events"`     // the events window (bells, notifications, ...)
	SwapBackground string `toml:"swap_background"` // swap the active window's program with the background's
	Repaint        string `toml:"repaint"`         // redraw the whole screen
	Settings       string `toml:"settings"`        // the settings dialog (local.toml)
}

// LayoutConfig holds window geometry settings.
type LayoutConfig struct {
	CascadeOffsetX       int     `toml:"cascade_offset_x"`
	CascadeOffsetY       int     `toml:"cascade_offset_y"`
	NewWindowWidthRatio  float64 `toml:"new_window_width_ratio"`
	NewWindowHeightRatio float64 `toml:"new_window_height_ratio"`
	MinWindowCols        int     `toml:"min_window_cols"`
	MinWindowRows        int     `toml:"min_window_rows"`

	// Accepted here too because float's example config put them in [layout].
	AltTimeoutMs   int `toml:"alt_timeout_ms"`
	PollIntervalMs int `toml:"poll_interval_ms"`
}

// Config is the whole configuration file.
type Config struct {
	Shell        string `toml:"shell"`
	DisableMouse bool   `toml:"disable_mouse"`
	// Window frame: "full" (box with a three-row title box), "compact" (box,
	// title in the top border) or "none" (only a top line with the title).
	Frame string `toml:"frame"`

	// After a lone Esc, a character key within this many milliseconds is
	// treated as Alt+key. 0 disables the Esc prefix.
	AltTimeoutMs int `toml:"alt_timeout_ms"`
	// macOS Terminal and iTerm2 type characters for Option+key by default
	// (Option+c = ç). "on" treats those characters as Alt+key when the key
	// is a shortcut, "off" never does, "auto" = on when running on macOS or
	// in Terminal.app / iTerm2.
	MacOptionKeys string `toml:"mac_option_keys"`
	// macOS Terminal sends Esc b / Esc f for Option+Left / Option+Right
	// (the same as Alt+b / Alt+f). "on" takes them as Alt+Left / Alt+Right
	// (move the window), "off" as Alt+b / Alt+f, "auto" = on in macOS
	// Terminal until an Alt+letter arrives as Esc + letter (Option as Meta).
	MacOptionArrows string `toml:"mac_option_arrows"`
	// Unused (goat is event driven); accepted for float compatibility.
	PollIntervalMs int `toml:"poll_interval_ms"`

	// Lines of history kept per window (mouse wheel / Alt+PageUp to view).
	ScrollbackLines int `toml:"scrollback_lines"`

	// Log of problems, warnings and crashes: a file ("" = the default,
	// $XDG_STATE_HOME/goat/goat.log or ~/.local/state/goat/goat.log;
	// "none" = no log) and how much goes in (error, warn, info, debug).
	LogFile  string `toml:"log_file"`
	LogLevel string `toml:"log_level"`

	// Wrap long lines at the window's edge (true), or let them run on and
	// scroll the view sideways (false; keys.toggle_wrap switches per window).
	// Not wrapping, a window holds lines up to nowrap_width columns.
	WrapLines   bool `toml:"wrap_lines"`
	NowrapWidth int  `toml:"nowrap_width"`

	// Show the active window's current directory in the status bar, left of
	// the clock (shortened from the left when it doesn't fit).
	StatusShowDir bool `toml:"status_show_dir"`
	// Show the git repository and branch of each window's directory in its
	// bottom border, after the host name ("📖nontechno/goat 🌿main"; see git.go).
	ShowGit bool `toml:"show_git"`

	// Show the host name on the left of each framed window's bottom border.
	// The name is the system's host name, or its alias from
	// ~/.config/goat/alias.toml (alias.go).
	ShowHost bool `toml:"show_host"`
	// Show the user name there too ("user@host"): the user of the window's
	// foreground program (root after su/sudo), or the user an ssh session
	// logs in as. Both are checked twice a second while shown.
	ShowUser bool `toml:"show_user"`

	// Where window titles come from: "process" (the foreground program's
	// name, like float) or "terminal" (the title the program sets with
	// OSC 0/2, falling back to the process name).
	TitleSource string `toml:"title_source"`

	// 24-bit color: "auto" (the terminal's environment says), "on" or
	// "off" (truecolor.go).
	TrueColor string `toml:"true_color"`

	// Copying selected text: via the terminal (OSC 52, works over SSH) and/or
	// by piping it to a command ("" = detect one: tmux, clip.exe, pbcopy,
	// wl-copy, xclip, xsel; "none" = no command).
	ClipboardOSC52 bool   `toml:"clipboard_osc52"`
	CopyCommand    string `toml:"copy_command"`

	// Repaint the whole screen every this many seconds (0 = never), besides
	// on keys.repaint and when the terminal gets focus back: repairs
	// whatever else wrote to the terminal or drew differently from goat.
	RepaintSeconds int `toml:"repaint_seconds"`

	Theme  ThemeConfig  `toml:"theme"`
	Keys   KeyConfig    `toml:"keys"`
	Layout LayoutConfig `toml:"layout"`

	bindings map[rune]action
	frame    frameStyle
}

// DefaultConfig returns the built-in configuration.
func DefaultConfig() *Config {
	c := &Config{
		AltTimeoutMs:    200,
		MacOptionKeys:   "auto",
		MacOptionArrows: "auto",
		PollIntervalMs:  16,
		ScrollbackLines: 1000,
		WrapLines:       true,
		LogLevel:        "info",
		NowrapWidth:     512,
		TitleSource:     "process",
		TrueColor:       "auto",
		Frame:           "full",
		ClipboardOSC52:  true,
		ShowHost:        true,
		ShowGit:         true,
		ShowUser:        true,
		Theme: ThemeConfig{
			FocusedBorder:       idx(14),
			UnfocusedBorder:     idx(8),
			PinnedBorder:        idx(11),
			HintText:            idx(11),
			StatusFg:            idx(250),
			StatusBg:            idx(236),
			ClockFg:             idx(231),
			ClockBg:             idx(24),
			NewWindowNextColors: true,
			MinTextContrast:     3,
		},
		Keys: KeyConfig{
			NewWindow: "c", FocusNext: "n", FocusPrev: "p", Quit: "q",
			CloseWindow: "x", PinWindow: "w", Palette: "t",
			ToggleWrap: "z", ScrollLeft: "<", ScrollRight: ">", ShowEvents: "-",
			SwapBackground: "s", Repaint: "r", Settings: "o",
			MoveLeft: "h", MoveDown: "j", MoveUp: "k", MoveRight: "l",
			ResizeLeft: "H", ResizeDown: "J", ResizeUp: "K", ResizeRight: "L",
		},
		Layout: LayoutConfig{
			CascadeOffsetX:       2,
			CascadeOffsetY:       1,
			NewWindowWidthRatio:  0.5,
			NewWindowHeightRatio: 0.5,
			MinWindowCols:        6,
			MinWindowRows:        5,
		},
	}
	_ = c.finish(nil) // defaults are always valid
	return c
}

type action int

const (
	actNone action = iota
	actNewWindow
	actFocusNext
	actFocusPrev
	actQuit
	actCloseWindow
	actMoveLeft
	actMoveDown
	actMoveUp
	actMoveRight
	actResizeLeft
	actResizeDown
	actResizeUp
	actResizeRight
	actPinWindow
	actPalette
	actToggleWrap
	actScrollLeft
	actScrollRight
	actShowEvents
	actSwapBackground
	actRepaint
	actSettings
)

// finish validates the config and builds the key binding table. Problems are
// appended to warnings; invalid values fall back to defaults.
func (c *Config) finish(md *toml.MetaData) []string {
	var warns []string
	warn := func(f string, a ...any) { warns = append(warns, fmt.Sprintf(f, a...)) }

	if md != nil {
		// float's example config put these under [layout]; honor them there
		// unless also set at the top level.
		if md.IsDefined("layout", "alt_timeout_ms") && !md.IsDefined("alt_timeout_ms") {
			c.AltTimeoutMs = c.Layout.AltTimeoutMs
		}
		for _, k := range md.Undecoded() {
			switch k.String() {
			case "keys.cycle_background", "keys.cycle_foreground":
				warn("%s is no longer used: Alt+%s (keys.palette) chooses a window's colors", k.String(), c.Keys.Palette)
			default:
				warn("unknown setting %q (ignored)", k.String())
			}
		}
	}

	if c.AltTimeoutMs < 0 {
		c.AltTimeoutMs = 0
	}
	if _, ok := parseLogLevel(c.LogLevel); !ok {
		warn("log_level = %q: use error, warn, info or debug (using info)", c.LogLevel)
		c.LogLevel = "info"
	}
	if c.NowrapWidth < 80 || c.NowrapWidth > 4096 {
		warn("nowrap_width = %d: want 80..4096 (using 512)", c.NowrapWidth)
		c.NowrapWidth = 512
	}
	if mc := c.Theme.MinTextContrast; mc != 0 && (mc < 1 || mc > 21) {
		warn("theme.min_text_contrast = %g: want 1..21, or 0 for no check (using 3)", mc)
		c.Theme.MinTextContrast = 3
	}
	if c.ScrollbackLines < 0 {
		c.ScrollbackLines = 0
	}
	if c.RepaintSeconds < 0 {
		warn("repaint_seconds %d: want 0 (never) or more", c.RepaintSeconds)
		c.RepaintSeconds = 0
	}
	if f, ok := parseFrame(c.Frame); ok {
		c.frame = f
	} else {
		warn("frame %q: want \"full\", \"compact\" or \"none\"", c.Frame)
		c.Frame, c.frame = "full", frameFull
	}
	switch c.TrueColor {
	case "auto", "on", "off":
	default:
		warn("true_color = %q: use \"auto\", \"on\" or \"off\" (using \"auto\")", c.TrueColor)
		c.TrueColor = "auto"
	}
	switch c.TitleSource {
	case "process", "terminal":
	default:
		warn("title_source %q: want \"process\" or \"terminal\"", c.TitleSource)
		c.TitleSource = "process"
	}
	l := &c.Layout
	if l.MinWindowCols < 6 {
		l.MinWindowCols = 6
	}
	if l.MinWindowRows < 3 {
		l.MinWindowRows = 3
	}
	clampRatio := func(name string, r *float64) {
		if *r <= 0 || *r > 1 {
			warn("layout.%s %.2f: want a value in (0, 1]", name, *r)
			*r = 0.5
		}
	}
	clampRatio("new_window_width_ratio", &l.NewWindowWidthRatio)
	clampRatio("new_window_height_ratio", &l.NewWindowHeightRatio)

	c.bindings = map[rune]action{}
	for _, b := range []struct {
		name string
		val  *string
		def  string
		act  action
	}{
		{"new_window", &c.Keys.NewWindow, "c", actNewWindow},
		{"focus_next", &c.Keys.FocusNext, "n", actFocusNext},
		{"focus_prev", &c.Keys.FocusPrev, "p", actFocusPrev},
		{"quit", &c.Keys.Quit, "q", actQuit},
		{"close_window", &c.Keys.CloseWindow, "x", actCloseWindow},
		{"move_left", &c.Keys.MoveLeft, "h", actMoveLeft},
		{"move_down", &c.Keys.MoveDown, "j", actMoveDown},
		{"move_up", &c.Keys.MoveUp, "k", actMoveUp},
		{"move_right", &c.Keys.MoveRight, "l", actMoveRight},
		{"resize_left", &c.Keys.ResizeLeft, "H", actResizeLeft},
		{"resize_down", &c.Keys.ResizeDown, "J", actResizeDown},
		{"resize_up", &c.Keys.ResizeUp, "K", actResizeUp},
		{"resize_right", &c.Keys.ResizeRight, "L", actResizeRight},
		{"pin_window", &c.Keys.PinWindow, "w", actPinWindow},
		{"palette", &c.Keys.Palette, "t", actPalette},
		{"toggle_wrap", &c.Keys.ToggleWrap, "z", actToggleWrap},
		{"scroll_left", &c.Keys.ScrollLeft, "<", actScrollLeft},
		{"scroll_right", &c.Keys.ScrollRight, ">", actScrollRight},
		{"show_events", &c.Keys.ShowEvents, "-", actShowEvents},
		{"swap_background", &c.Keys.SwapBackground, "s", actSwapBackground},
		{"repaint", &c.Keys.Repaint, "r", actRepaint},
		{"settings", &c.Keys.Settings, "o", actSettings},
	} {
		r, size := utf8.DecodeRuneInString(*b.val)
		if *b.val == "" || size != len(*b.val) || (r >= '0' && r <= '9') {
			warn("keys.%s %q: want a single non-digit character (using %q)", b.name, *b.val, b.def)
			*b.val = b.def
			r = rune(b.def[0])
		}
		if prev, dup := c.bindings[r]; dup && prev != b.act {
			warn("keys.%s %q is already bound; ignoring", b.name, *b.val)
			continue
		}
		c.bindings[r] = b.act
	}
	switch c.MacOptionKeys {
	case "auto", "on", "off":
	default:
		warn("mac_option_keys = %q: use \"auto\", \"on\" or \"off\" (using \"auto\")", c.MacOptionKeys)
		c.MacOptionKeys = "auto"
	}
	switch c.MacOptionArrows {
	case "auto", "on", "off":
	default:
		warn("mac_option_arrows = %q: use \"auto\", \"on\" or \"off\" (using \"auto\")", c.MacOptionArrows)
		c.MacOptionArrows = "auto"
	}
	return warns
}

// configPaths returns candidate config files in priority order.
func configPaths() []string {
	var base []string
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		base = append(base, x)
	}
	if h, err := os.UserHomeDir(); err == nil {
		base = append(base, filepath.Join(h, ".config"))
	}
	var out []string
	for _, app := range []string{"goat", "float"} { // float's config works too
		for _, b := range base {
			out = append(out, filepath.Join(b, app, "config.toml"))
		}
	}
	return out
}

// LoadConfig loads the first config file found. It never fails: problems are
// returned as warnings and defaults are used for anything unusable.
func LoadConfig(explicit string) (*Config, string, []string) {
	paths := configPaths()
	if explicit != "" {
		paths = []string{explicit}
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) && explicit == "" {
			continue
		}
		if err != nil {
			return DefaultConfig(), p, []string{fmt.Sprintf("reading %s: %v", p, err)}
		}
		cfg, warns := parseConfig(string(data))
		for i := range warns {
			warns[i] = p + ": " + warns[i]
		}
		return cfg, p, warns
	}
	return DefaultConfig(), "", nil
}

func parseConfig(data string) (*Config, []string) {
	cfg := DefaultConfig()
	md, err := toml.Decode(data, cfg)
	if err != nil {
		return DefaultConfig(), []string{"parse error, using defaults: " + err.Error()}
	}
	return cfg, cfg.finish(&md)
}
