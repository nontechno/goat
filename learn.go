package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/term"
)

// "goat -learn": find out how this terminal sends goat's keys by asking for
// a few of them, and save what was learned in local.toml, per terminal:
//
//	[terminal."Apple_Terminal"]
//	# learned by goat -learn on 2026-10-13
//	mac_option_keys = "on"
//	emoji = true
//	remap = { "alt+b" = "alt+left", "alt+f" = "alt+right" }
//
// mac_option_keys: whether Option+letters arrive as characters (ç for
// Option+c), to be taken as Alt+letter. remap: keys the terminal sends
// differently from what goat expects (here Option+Left arriving as Alt+b).
// emoji: whether the terminal draws 🧑 / 💻 two columns wide (if not, window
// frames show user@host). A profile replaces the "auto" guesses of
// mac_option_keys / mac_option_arrows; "on" / "off" in config.toml still win.

// learnStep is one key the user is asked to press.
type learnStep struct {
	want   string // the key goat expects, as uv.Key.Keystroke() writes it
	prompt string // what to press
	mac    string // the same on a Mac keyboard
}

func learnSteps(cfg *Config) []learnStep {
	k := cfg.Keys
	return []learnStep{
		{"alt+" + k.NewWindow, "Alt+" + k.NewWindow + " (new window)", "Option+" + k.NewWindow},
		{"alt+" + k.CycleBackground, "Alt+" + k.CycleBackground + " (background color)", "Option+" + k.CycleBackground},
		{"alt+" + k.CycleForeground, "Alt+" + k.CycleForeground + " (text color)", "Option+" + k.CycleForeground},
		{"alt+left", "Alt+Left (move window left)", "Option+←"},
		{"alt+right", "Alt+Right (move window right)", "Option+→"},
		{"alt+up", "Alt+Up (move window up)", "Option+↑"},
		{"alt+down", "Alt+Down (move window down)", "Option+↓"},
		{"alt+shift+left", "Alt+Shift+Left (narrower)", "Option+Shift+←"},
	}
}

// termProfile is what goat knows about one terminal.
type termProfile struct {
	MacOptionKeys string            `toml:"mac_option_keys"`
	Emoji         *bool             `toml:"emoji"`
	Remap         map[string]string `toml:"remap"`
}

// terminalID names the terminal goat runs in, as the profile's key: what it
// says it is (TERM_PROGRAM; LC_TERMINAL, which iTerm2 also sets over ssh),
// else "unknown".
func terminalID() string {
	id := os.Getenv("TERM_PROGRAM")
	if id == "" || id == "tmux" || id == "screen" {
		if lc := os.Getenv("LC_TERMINAL"); lc != "" {
			id = lc
		}
	}
	if id == "" {
		id = "unknown"
	}
	return regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(id, "_")
}

// readTermProfile reads the profile for terminal id from local.toml.
func readTermProfile(path, id string) (termProfile, bool, string) {
	data, err := readSmallFile(path, 64<<10)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || path == "" {
			return termProfile{}, false, ""
		}
		return termProfile{}, false, fmt.Sprintf("%s: %v", path, err)
	}
	var lc struct {
		Terminal map[string]termProfile `toml:"terminal"`
	}
	if _, err := toml.Decode(string(data), &lc); err != nil {
		return termProfile{}, false, fmt.Sprintf("%s: %v (terminal profile not used)", path, err)
	}
	p, ok := lc.Terminal[id]
	return p, ok, ""
}

// writeTermProfile puts the profile for terminal id into local.toml,
// replacing that terminal's section and keeping everything else.
func writeTermProfile(path, id string, p termProfile) error {
	old, err := readSmallFile(path, 64<<10)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	name := fmt.Sprintf("terminal.%q", id)
	section := []string{"[" + name + "]",
		"# learned by goat -learn on " + time.Now().Format("2006-01-02") + ": how this terminal sends keys",
		fmt.Sprintf("mac_option_keys = %q", p.MacOptionKeys)}
	if p.Emoji != nil {
		section = append(section, fmt.Sprintf("emoji = %v", *p.Emoji))
	}
	if len(p.Remap) > 0 {
		var kv []string
		for _, from := range sortedKeys(p.Remap) {
			kv = append(kv, fmt.Sprintf("%q = %q", from, p.Remap[from]))
		}
		section = append(section, "remap = { "+strings.Join(kv, ", ")+" }")
	}
	out := replaceSection(strings.Split(string(old), "\n"), name, section)
	return writeFileAtomic(path, []byte(out), 0o644)
}

func sortedKeys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// decodeKey is the key goat makes of the bytes a key press sent ("" if
// they aren't one key).
func decodeKey(b []byte) string {
	var d uv.EventDecoder
	n, ev := d.Decode(b)
	k, ok := ev.(uv.KeyPressEvent)
	if !ok || n != len(b) {
		return ""
	}
	return uv.Key(k).Keystroke()
}

// learnResult is what the key presses showed.
type learnResult struct {
	profile   termProfile
	notes     []string    // things to tell the user
	conflicts [][2]string // two wanted keys that arrive as the same key
}

// analyzeLearn works out a profile from what each wanted key arrived as
// (got[want] = goat's reading of the bytes; "" = skipped or unreadable).
func analyzeLearn(steps []learnStep, got map[string]string) learnResult {
	var r learnResult
	r.profile.MacOptionKeys = "off"
	r.profile.Remap = map[string]string{}

	// Option characters: if a letter shortcut arrived as one (ç), Option
	// types characters; goat then reads them as Alt+letter.
	asMac := func(k string) string {
		rs := []rune(k)
		if len(rs) != 1 {
			return k
		}
		if alt, ok := macOptionKey(uv.Key{Code: rs[0], Text: k}); ok {
			return alt.Keystroke()
		}
		return k
	}
	for _, s := range steps {
		if g := got[s.want]; g != "" && asMac(g) != g {
			r.profile.MacOptionKeys = "on"
		}
	}
	norm := func(k string) string {
		if r.profile.MacOptionKeys == "on" {
			return asMac(k)
		}
		return k
	}

	// Which wanted keys arrive as the very same key (judged on what the
	// terminal sent: ∫ and Esc b both stand for Alt+b, but are different).
	by := map[string][]string{}
	for _, s := range steps {
		if g := got[s.want]; g != "" {
			by[g] = append(by[g], s.want)
		}
	}
	for _, s := range steps {
		g := got[s.want]
		switch {
		case g == "":
			r.notes = append(r.notes, fmt.Sprintf("%s: skipped", s.prompt))
		case norm(g) == s.want:
			// arrives as expected (or as an Option character for it)
		case len(by[g]) > 1:
			// a conflict: the user chooses, below
		case !strings.Contains(g, "+"):
			r.notes = append(r.notes, fmt.Sprintf("%s arrives as plain %q: goat can't tell it from that key; change the terminal's setting for it", s.prompt, g))
		default:
			r.profile.Remap[g] = s.want
		}
	}
	seen := map[string]bool{}
	for _, s := range steps {
		g := got[s.want]
		if g == "" || len(by[g]) < 2 || seen[g] {
			continue
		}
		seen[g] = true
		r.conflicts = append(r.conflicts, [2]string{by[g][0], by[g][1]})
	}
	return r
}

// resolveConflict records the user's choice for two keys that arrive the
// same: the key they both arrive as does what `keep` does.
func (r *learnResult) resolveConflict(arrivesAs, keep string) {
	if arrivesAs == keep {
		delete(r.profile.Remap, arrivesAs)
		return
	}
	r.profile.Remap[arrivesAs] = keep
}

// learn runs "goat -learn".
func learn(cfg *Config, in *os.File, out io.Writer) error {
	id := terminalID()
	path := localConfigPath()
	if path == "" {
		return errors.New("can't find the config folder")
	}
	steps := learnSteps(cfg)
	fmt.Fprintf(out, "goat -learn: how does this terminal (%s) send goat's keys?\n", id)
	fmt.Fprintf(out, "Press each key when asked; Enter skips one. Ctrl+C stops (nothing is saved).\n\n")

	state, err := term.MakeRaw(in.Fd())
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	restored := false
	restore := func() {
		if !restored {
			_ = term.Restore(in.Fd(), state)
			restored = true
		}
	}
	defer restore()
	keys := keyReader(in)
	nl := "\r\n"

	got := map[string]string{}
	raw := map[string][]byte{}
	for i, s := range steps {
		fmt.Fprintf(out, "%d/%d  press %s (on a Mac: %s) ... ", i+1, len(steps), s.prompt, s.mac)
		b, ok := <-keys
		if !ok {
			return errors.New("input closed")
		}
		switch {
		case len(b) == 1 && b[0] == 0x03:
			fmt.Fprint(out, nl+"stopped; nothing saved"+nl)
			return nil
		case len(b) == 1 && (b[0] == '\r' || b[0] == '\n'):
			fmt.Fprint(out, "skipped"+nl)
			continue
		}
		raw[s.want] = append([]byte(nil), b...)
		got[s.want] = decodeKey(b)
		fmt.Fprintf(out, "%q%s", b, nl)
	}

	r := analyzeLearn(steps, got)
	for _, c := range r.conflicts {
		arrives := got[c[0]]
		fmt.Fprintf(out, "%s%s and %s send the same thing (%q).%s", nl, keyName(steps, c[0]), keyName(steps, c[1]), raw[c[0]], nl)
		fmt.Fprintf(out, "  1  use it for %s%s  2  use it for %s%s", keyName(steps, c[0]), nl, keyName(steps, c[1]), nl)
		fmt.Fprintf(out, "  (to have both, change one of them in the terminal's keyboard settings and run goat -learn again)%s", nl)
		fmt.Fprint(out, "  choose 1 or 2: ")
		for {
			b, ok := <-keys
			if !ok {
				return errors.New("input closed")
			}
			if len(b) == 1 && b[0] == 0x03 {
				fmt.Fprint(out, nl+"stopped; nothing saved"+nl)
				return nil
			}
			if len(b) == 1 && (b[0] == '1' || b[0] == '2') {
				keep := c[int(b[0]-'1')]
				r.resolveConflict(arrives, keep)
				fmt.Fprintf(out, "%c -> %s%s", b[0], keyName(steps, keep), nl)
				break
			}
		}
	}

	// How wide the terminal draws the frame's emoji: print one and ask
	// where the cursor went.
	if w, ok := measureWidth(in, out, keys, userSymbol+hostSymbol); ok {
		emoji := w == 4
		r.profile.Emoji = &emoji
		if !emoji {
			r.notes = append(r.notes, "this terminal doesn't draw 🧑 / 💻 two columns wide: window frames will show user@host")
		}
	}
	restore()

	fmt.Fprintln(out)
	for _, n := range r.notes {
		fmt.Fprintln(out, "note:", n)
	}
	if err := writeTermProfile(path, id, r.profile); err != nil {
		return err
	}
	fmt.Fprintf(out, "saved in %s, section [terminal.%q]:\n", path, id)
	fmt.Fprintf(out, "  mac_option_keys = %q", r.profile.MacOptionKeys)
	if r.profile.MacOptionKeys == "on" {
		fmt.Fprint(out, " (Option+letters type characters; goat takes them as Alt+letter)")
	}
	fmt.Fprintln(out)
	for _, from := range sortedKeys(r.profile.Remap) {
		fmt.Fprintf(out, "  %s is read as %s\n", from, r.profile.Remap[from])
	}
	if len(r.profile.Remap) == 0 {
		fmt.Fprintln(out, "  every key arrives as goat expects")
	}
	fmt.Fprintln(out, "goat uses this whenever it runs in this terminal; run goat -learn again after changing the terminal's settings.")
	return nil
}

func keyName(steps []learnStep, want string) string {
	for _, s := range steps {
		if s.want == want {
			return s.prompt + " / " + s.mac
		}
	}
	return want
}

// keyReader delivers what each key press sent: the bytes of one read, plus
// anything following within a few milliseconds (the rest of a sequence).
func keyReader(in io.Reader) <-chan []byte {
	chunks := make(chan []byte)
	go func() {
		defer close(chunks)
		buf := make([]byte, 256)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				chunks <- append([]byte(nil), buf[:n]...)
			}
			if err != nil {
				return
			}
		}
	}()
	keys := make(chan []byte)
	go func() {
		defer close(keys)
		for b := range chunks {
			for more := true; more; {
				select {
				case c, ok := <-chunks:
					if !ok {
						more = false
						break
					}
					b = append(b, c...)
				case <-time.After(40 * time.Millisecond):
					more = false
				}
			}
			keys <- b
		}
	}()
	return keys
}

// measureWidth prints s at the start of a line and asks the terminal where
// the cursor is (CSI 6n): the column tells how wide s was drawn.
func measureWidth(in *os.File, out io.Writer, keys <-chan []byte, s string) (int, bool) {
	fmt.Fprint(out, "\r\x1b[K"+s+"\x1b[6n")
	defer fmt.Fprint(out, "\r\x1b[K")
	if f, ok := out.(*os.File); ok {
		_ = f.Sync()
	}
	timeout := time.After(time.Second)
	for {
		select {
		case b, ok := <-keys:
			if !ok {
				return 0, false
			}
			if col, ok := parseCPR(b); ok {
				return col - 1, true
			}
		case <-timeout:
			return 0, false
		}
	}
}

var cprRe = regexp.MustCompile(`\x1b\[(\d+);(\d+)R`)

func parseCPR(b []byte) (int, bool) {
	m := cprRe.FindSubmatch(b)
	if m == nil {
		return 0, false
	}
	col, err := strconv.Atoi(string(m[2]))
	return col, err == nil
}

// ---- at runtime --------------------------------------------------------------

// keyFromKeystroke builds the key a remap target names ("alt+left",
// "alt+shift+left", "alt+b").
func keyFromKeystroke(s string) (uv.Key, bool) {
	var k uv.Key
	parts := strings.Split(s, "+")
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "alt":
			k.Mod |= uv.ModAlt
		case "shift":
			k.Mod |= uv.ModShift
		case "ctrl":
			k.Mod |= uv.ModCtrl
		default:
			return k, false
		}
	}
	name := parts[len(parts)-1]
	switch name {
	case "left":
		k.Code = uv.KeyLeft
	case "right":
		k.Code = uv.KeyRight
	case "up":
		k.Code = uv.KeyUp
	case "down":
		k.Code = uv.KeyDown
	default:
		rs := []rune(name)
		if len(rs) != 1 {
			return k, false
		}
		k.Code = rs[0]
	}
	return k, true
}

// applyTermProfile puts a learned profile to use (config.toml's explicit
// "on" / "off" settings stay as they are).
func (m *WM) applyTermProfile(p termProfile) {
	if m.cfg.MacOptionKeys == "auto" && p.MacOptionKeys != "" {
		m.macOption = p.MacOptionKeys == "on"
	}
	if m.cfg.MacOptionArrows == "auto" {
		m.optionArrows, m.arrowsLearn = false, false // the remap says it all
	}
	m.keyRemap = map[string]uv.Key{}
	for from, to := range p.Remap {
		if k, ok := keyFromKeystroke(to); ok {
			m.keyRemap[from] = k
		}
	}
	if p.Emoji != nil && !*p.Emoji {
		m.plainIdent = true
	}
	m.profiled = true
}

// remapKey turns a key the terminal sends differently (per the learned
// profile) into the key it stands for.
func (m *WM) remapKey(k uv.Key) uv.Key {
	if len(m.keyRemap) == 0 {
		return k
	}
	if to, ok := m.keyRemap[k.Keystroke()]; ok {
		return to
	}
	return k
}
