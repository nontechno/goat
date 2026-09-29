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

// ColorPair is a window color scheme: background and default text color.
// In the config it is "back:text" (e.g. "17:230", "#1e1e2e:#cdd6f4",
// "default:default"), a table {bg = 17, fg = 230}, or a single color for the
// background alone.
type ColorPair struct{ Bg, Fg color.Color }

// UnmarshalTOML accepts "back:text", a single color, or {bg, fg}.
func (p *ColorPair) UnmarshalTOML(v any) error {
	var err error
	switch v := v.(type) {
	case int64:
		var c Color
		err = c.UnmarshalTOML(v)
		p.Bg, p.Fg = c.C, nil
	case string:
		back, text, _ := strings.Cut(v, ":")
		if p.Bg, err = parseColorString(back); err == nil {
			p.Fg, err = parseColorString(text)
		}
	case map[string]any:
		var bg, fg Color
		for k, val := range v {
			switch k {
			case "bg":
				err = bg.UnmarshalTOML(val)
			case "fg":
				err = fg.UnmarshalTOML(val)
			default:
				err = fmt.Errorf("unknown key %q (want bg, fg)", k)
			}
			if err != nil {
				break
			}
		}
		p.Bg, p.Fg = bg.C, fg.C
	default:
		err = fmt.Errorf("want \"back:text\", a color, or {bg, fg}; got %v", v)
	}
	return err
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

func pair(bg, fg uint8) ColorPair {
	return ColorPair{Bg: ansi.IndexedColor(bg), Fg: ansi.IndexedColor(fg)}
}

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

	// Color schemes (background:text) Alt+b steps the active window through.
	WindowColors []ColorPair `toml:"window_colors"`
	// Older background-only form; used when window_colors isn't set.
	WindowBackgrounds []Color `toml:"window_backgrounds"`
	// New windows (Alt+c) take the scheme after the focused window's in
	// window_colors, so they don't look like the window they came from.
	NewWindowNextColors bool `toml:"new_window_next_colors"`
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

	CycleBackground string `toml:"cycle_background"` // next of theme.window_colors
	ToggleWrap      string `toml:"toggle_wrap"`      // wrap long lines or not
	ScrollLeft      string `toml:"scroll_left"`      // view sideways (no-wrap)
	ScrollRight     string `toml:"scroll_right"`
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
	// Older form of frame: true = "compact", false = "full".
	Compact bool `toml:"compact"`

	// After a lone Esc, a character key within this many milliseconds is
	// treated as Alt+key. 0 disables the Esc prefix.
	AltTimeoutMs int `toml:"alt_timeout_ms"`
	// macOS Terminal and iTerm2 type characters for Option+key by default
	// (Option+c = ç). "on" treats those characters as Alt+key when the key
	// is a shortcut, "off" never does, "auto" = on when running on macOS or
	// in Terminal.app / iTerm2.
	MacOptionKeys string `toml:"mac_option_keys"`
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

	// Where window titles come from: "process" (the foreground program's
	// name, like float) or "terminal" (the title the program sets with
	// OSC 0/2, falling back to the process name).
	TitleSource string `toml:"title_source"`

	// Copying selected text: via the terminal (OSC 52, works over SSH) and/or
	// by piping it to a command ("" = detect one: tmux, clip.exe, pbcopy,
	// wl-copy, xclip, xsel; "none" = no command).
	ClipboardOSC52 bool   `toml:"clipboard_osc52"`
	CopyCommand    string `toml:"copy_command"`

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
		PollIntervalMs:  16,
		ScrollbackLines: 1000,
		WrapLines:       true,
		LogLevel:        "info",
		NowrapWidth:     512,
		TitleSource:     "process",
		Frame:           "full",
		ClipboardOSC52:  true,
		Theme: ThemeConfig{
			FocusedBorder:   idx(14),
			UnfocusedBorder: idx(8),
			PinnedBorder:    idx(11),
			HintText:        idx(11),
			StatusFg:        idx(250),
			StatusBg:        idx(236),
			ClockFg:         idx(231),
			ClockBg:         idx(24),
			WindowColors: []ColorPair{
				{},             // terminal default
				pair(234, 252), // charcoal / light grey
				pair(17, 230),  // navy / cream
				pair(22, 194),  // forest / pale green
				pair(52, 224),  // maroon / pale pink
				pair(53, 225),  // plum / lavender
				pair(58, 229),  // olive / pale yellow
				pair(23, 195),  // teal / pale cyan
				pair(25, 189),  // cobalt / periwinkle
				pair(94, 223),  // brown / wheat
				pair(54, 183),  // indigo / light violet
				pair(130, 230), // rust / cream
				pair(29, 157),  // sea green / mint
			},
			NewWindowNextColors: true,
		},
		Keys: KeyConfig{
			NewWindow: "c", FocusNext: "n", FocusPrev: "p", Quit: "q",
			CloseWindow: "x", PinWindow: "w", CycleBackground: "b",
			ToggleWrap: "z", ScrollLeft: "<", ScrollRight: ">",
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
	actCycleBackground
	actToggleWrap
	actScrollLeft
	actScrollRight
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
		// compact = true/false is honored when frame isn't given.
		if md.IsDefined("compact") && !md.IsDefined("frame") {
			c.Frame = map[bool]string{true: "compact", false: "full"}[c.Compact]
		}
		// window_backgrounds (background only) is honored when window_colors
		// isn't given.
		if md.IsDefined("theme", "window_backgrounds") && !md.IsDefined("theme", "window_colors") {
			c.Theme.WindowColors = nil
			for _, b := range c.Theme.WindowBackgrounds {
				c.Theme.WindowColors = append(c.Theme.WindowColors, ColorPair{Bg: b.C})
			}
		}
		for _, k := range md.Undecoded() {
			warn("unknown setting %q (ignored)", k.String())
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
	if c.ScrollbackLines < 0 {
		c.ScrollbackLines = 0
	}
	if f, ok := parseFrame(c.Frame); ok {
		c.frame = f
	} else {
		warn("frame %q: want \"full\", \"compact\" or \"none\"", c.Frame)
		c.Frame, c.frame = "full", frameFull
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
		{"cycle_background", &c.Keys.CycleBackground, "b", actCycleBackground},
		{"toggle_wrap", &c.Keys.ToggleWrap, "z", actToggleWrap},
		{"scroll_left", &c.Keys.ScrollLeft, "<", actScrollLeft},
		{"scroll_right", &c.Keys.ScrollRight, ">", actScrollRight},
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
