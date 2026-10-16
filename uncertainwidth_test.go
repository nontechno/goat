package main

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/rivo/uniseg"
)

// simTerm is a small model of a host terminal: enough of the escape
// sequences goat's renderer sends, with its own idea of character widths.
type simTerm struct {
	w, h     int
	cells    [][]string // "" = the right half of a wide character
	x, y     int
	phantom  bool
	autowrap bool
	width    func(cluster string) int
	bad      string // the first thing a terminal shouldn't have been sent
	top, bot int    // scroll region (bot exclusive; 0, 0 = whole screen)

	// State another program could leave behind: character sets (G0, G1,
	// which one is in use), insert mode, origin mode.
	g        [2]byte // '0' = DEC line drawing, else ASCII
	shifted  bool    // SO: G1 in use
	insert   bool
	origin   bool
	injected bool // writing garbage: no complaints
}

// decGraphics is the DEC special graphics set, for the letters that differ.
var decGraphics = map[byte]string{'j': "┘", 'k': "┐", 'l': "┌", 'm': "└", 'n': "┼", 'q': "─", 't': "├", 'u': "┤", 'v': "┴", 'w': "┬", 'x': "│"}

// inject writes to the terminal as some other program would.
func (t *simTerm) inject(tb testing.TB, b []byte) {
	t.injected = true
	t.write(tb, b)
	t.injected = false
}

func (t *simTerm) region() (int, int) {
	if t.bot <= t.top {
		return 0, t.h
	}
	return t.top, t.bot
}

func (t *simTerm) blankRow() []string {
	row := make([]string, t.w)
	for x := range row {
		row[x] = " "
	}
	return row
}

// scrollUp moves rows from..to-1 up n, blank rows coming in at the bottom.
func (t *simTerm) scrollUp(from, to, n int) {
	for range n {
		copy(t.cells[from:to], t.cells[from+1:to])
		t.cells[to-1] = t.blankRow()
	}
}

func (t *simTerm) scrollDown(from, to, n int) {
	for range n {
		copy(t.cells[from+1:to], t.cells[from:to-1])
		t.cells[from] = t.blankRow()
	}
}

func newSimTerm(w, h int, width func(string) int) *simTerm {
	t := &simTerm{w: w, h: h, autowrap: true, width: width}
	t.cells = make([][]string, h)
	for y := range t.cells {
		t.cells[y] = make([]string, w)
		for x := range t.cells[y] {
			t.cells[y][x] = " "
		}
	}
	return t
}

// clear blanks column x of row y, and the rest of a wide character there.
func (t *simTerm) clear(x, y int) {
	if x < 0 || x >= t.w {
		return
	}
	row := t.cells[y]
	if row[x] == "" && x > 0 { // right half: the whole character goes
		row[x-1] = " "
	}
	if x+1 < t.w && row[x+1] == "" {
		row[x+1] = " "
	}
	row[x] = " "
}

func (t *simTerm) newline() {
	top, bot := t.region()
	if t.y == bot-1 {
		t.scrollUp(top, bot, 1)
		return
	}
	if t.y < t.h-1 {
		t.y++
	}
}

func (t *simTerm) print(cl string) {
	if set := t.g[pick(t.shifted, 1, 0)]; set == '0' && len(cl) == 1 && decGraphics[cl[0]] != "" {
		cl = decGraphics[cl[0]]
	}
	n := t.width(cl)
	if n <= 0 {
		return
	}
	if t.insert && !t.phantom { // the rest of the line moves right
		row := t.cells[t.y]
		copy(row[min(t.x+n, t.w):], row[t.x:max(t.w-n, t.x)])
	}
	if t.phantom && t.autowrap {
		t.x = 0
		t.newline()
	}
	t.phantom = false
	if t.x+n > t.w { // doesn't fit
		if !t.autowrap {
			return // dropped
		}
		t.x = 0
		t.newline()
	}
	for i := range n {
		t.clear(t.x+i, t.y)
	}
	t.cells[t.y][t.x] = cl
	for i := 1; i < n; i++ {
		t.cells[t.y][t.x+i] = ""
	}
	t.x += n
	if t.x >= t.w {
		t.x = t.w - 1
		t.phantom = true
	}
}

func (t *simTerm) clamp() {
	t.x = max(0, min(t.x, t.w-1))
	t.y = max(0, min(t.y, t.h-1))
	t.phantom = false
}

func (t *simTerm) write(tb testing.TB, b []byte) {
	tb.Helper()
	for len(b) > 0 {
		switch c := b[0]; {
		case c == 0x1b:
			n := t.escape(tb, b)
			b = b[n:]
		case c == '\r':
			t.x, t.phantom = 0, false
			b = b[1:]
		case c == '\n':
			t.newline()
			t.phantom = false
			b = b[1:]
		case c == '\b':
			t.x = max(t.x-1, 0)
			t.phantom = false
			b = b[1:]
		case c == '\t':
			t.x = min((t.x/8+1)*8, t.w-1)
			b = b[1:]
		case c == 0x0e, c == 0x0f: // SO, SI
			t.shifted = c == 0x0e
			b = b[1:]
		case c < 0x20 || c == 0x7f:
			if t.bad == "" && !t.injected {
				t.bad = fmt.Sprintf("control character %q in output", c)
			}
			b = b[1:]
		default:
			cl, rest, _, _ := uniseg.FirstGraphemeCluster(b, -1)
			if t.bad == "" && !t.injected {
				if !utf8.Valid(cl) {
					t.bad = fmt.Sprintf("invalid UTF-8 %q in output", cl)
				}
				for _, r := range string(cl) {
					if (r >= 0x80 && r < 0xa0) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) ||
						r == 0x2028 || r == 0x2029 || r == 0x200e || r == 0x200f || r == 0x061c {
						t.bad = fmt.Sprintf("control or bidi character %q in output", r)
					}
				}
			}
			t.print(string(cl))
			b = rest
		}
	}
}

func (t *simTerm) escape(tb testing.TB, b []byte) int {
	tb.Helper()
	if len(b) < 2 {
		return len(b)
	}
	switch b[1] {
	case '[':
		i := 2
		for i < len(b) && (b[i] < 0x40 || b[i] > 0x7e) {
			i++
		}
		if i >= len(b) {
			tb.Fatalf("unterminated CSI %q", b)
		}
		t.csi(tb, string(b[2:i]), b[i])
		return i + 1
	case ']', 'P', '_', '^': // strings, to BEL or ST
		for i := 2; i < len(b); i++ {
			if b[i] == 0x07 {
				return i + 1
			}
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
				return i + 2
			}
		}
		return len(b)
	case 'M': // reverse index
		if t.y > 0 {
			t.y--
		}
		return 2
	case '7', '8', '=', '>':
		return 2
	case '(', ')':
		if len(b) > 2 {
			t.g[b[1]-'('] = b[2]
		}
		return 3
	}
	tb.Fatalf("unknown escape %q", b[:2])
	return 2
}

func (t *simTerm) csi(tb testing.TB, params string, final byte) {
	tb.Helper()
	private := strings.HasPrefix(params, "?")
	params = strings.TrimLeft(params, "?<>=")
	var ps []int
	for _, p := range strings.Split(params, ";") {
		n, _ := strconv.Atoi(p)
		ps = append(ps, n)
	}
	arg := func(i, def int) int {
		if i < len(ps) && ps[i] != 0 {
			return ps[i]
		}
		return def
	}
	switch final {
	case 'H', 'f':
		t.y, t.x = arg(0, 1)-1, arg(1, 1)-1
		if t.origin {
			t.y += t.top
		}
		t.clamp()
	case 'G', '`':
		t.x = arg(0, 1) - 1
		t.clamp()
	case 'd':
		t.y = arg(0, 1) - 1
		t.clamp()
	case 'A':
		t.y -= arg(0, 1)
		t.clamp()
	case 'B':
		t.y += arg(0, 1)
		t.clamp()
	case 'C':
		t.x += arg(0, 1)
		t.clamp()
	case 'D':
		t.x -= arg(0, 1)
		t.clamp()
	case 'X': // ECH
		for i := range arg(0, 1) {
			t.clear(t.x+i, t.y)
		}
	case 'K':
		from, to := t.x, t.w
		switch arg(0, 0) {
		case 1:
			from, to = 0, t.x+1
		case 2:
			from = 0
		}
		for x := from; x < to; x++ {
			t.clear(x, t.y)
		}
	case 'J':
		for y := range t.h {
			for x := range t.w {
				if arg(0, 0) == 2 || y > t.y || (y == t.y && x >= t.x) {
					t.clear(x, y)
				}
			}
		}
	case 'h', 'l':
		for _, p := range ps {
			switch {
			case private && p == 7:
				t.autowrap = final == 'h'
			case private && p == 6:
				t.origin = final == 'h'
			case !private && p == 4:
				t.insert = final == 'h'
			}
		}
	case 'P', '@': // delete / insert characters: the rest of the line shifts
		n := min(arg(0, 1), t.w-t.x)
		t.clear(t.x, t.y) // splits a wide character at the cursor
		row := t.cells[t.y]
		if final == 'P' {
			copy(row[t.x:], row[t.x+n:])
			for x := t.w - n; x < t.w; x++ {
				row[x] = " "
			}
		} else {
			copy(row[t.x+n:], row[t.x:t.w-n])
			for x := t.x; x < t.x+n; x++ {
				row[x] = " "
			}
		}
		for x := range t.w { // half characters left over
			if row[x] == "" && (x == 0 || t.width(row[x-1]) < 2 || row[x-1] == "") {
				row[x] = " "
			}
		}
		if row[t.w-1] != "" && t.width(row[t.w-1]) > 1 {
			row[t.w-1] = " "
		}
	case 'L', 'M': // insert / delete lines at the cursor, within the region
		top, bot := t.region()
		if t.y < top || t.y >= bot {
			break // outside the scroll region: nothing happens
		}
		if final == 'L' {
			t.scrollDown(t.y, bot, arg(0, 1))
		} else {
			t.scrollUp(t.y, bot, arg(0, 1))
		}
		t.x = 0
	case 'S':
		top, bot := t.region()
		t.scrollUp(top, bot, arg(0, 1))
	case 'T':
		top, bot := t.region()
		t.scrollDown(top, bot, arg(0, 1))
	case 'r':
		t.top, t.bot = arg(0, 1)-1, arg(1, t.h)
		if t.top == 0 && t.bot == t.h {
			t.top, t.bot = 0, 0
		}
		t.x, t.y = 0, 0
	case 'm', 'q', 'n', 'c', 't', 'u':
	default:
		tb.Fatalf("unhandled CSI %q %c", params, final)
	}
}

// Host terminals that disagree with goat about some widths.
var simWidths = map[string]func(string) int{
	// Variation selectors don't widen: "⚙️" is one column (macOS Terminal).
	"vs16-narrow": func(s string) int {
		r, _ := utf8.DecodeRuneInString(s)
		if strings.ContainsRune(s, 0xFE0F) {
			return uniseg.StringWidth(string(r))
		}
		return uniseg.StringWidth(s)
	},
	// Symbols are two columns (ambiguous-as-wide, or drawn as emoji).
	"symbols-wide": func(s string) int {
		if uv.UncertainWidth(s) {
			return 2
		}
		return uniseg.StringWidth(s)
	},
}

const symbolText = "■⚙️\r\n⚠️❌✅ℹ️⛔☢️☣️\r\n⚓\r\n⚡\r\n⚖️🛠️\r\n⬆️⬇️🔀\r\n\r\n✔ ok\r\n↩ ↪ x"

// Every cell goat draws lands where goat put it on the host, whatever the
// host thinks of the symbols' widths; only the symbols themselves may look
// different. Above all, the window's frame stays whole.
func TestUncertainWidthStaysInWindow(t *testing.T) {
	for name, width := range simWidths {
		t.Run(name, func(t *testing.T) {
			m := newWM(DefaultConfig(), nil, 40, 14)
			bg := fakeWindow(0, 0, 0, 40, 13, false, false)
			bg.background = true
			bg.emu.Resize(bg.contentW(), bg.contentH())
			a := fakeWindow(1, 3, 1, 16, 11, true, false)
			m.windows, m.bg = []*Window{bg, a}, bg
			m.focus(a)
			_, _ = bg.emu.Write([]byte(strings.Repeat("background text here, more of it\r\n", 12)))

			var out bytes.Buffer
			s := newHostScreen(&out, []string{"TERM=xterm-256color"}, 40, 14)
			host := newSimTerm(40, 14, width)
			frame := func(step string) {
				t.Helper()
				out.Reset()
				if err := s.render(scene{m}, cursorState{}); err != nil {
					t.Fatal(err)
				}
				host.write(t, out.Bytes())
				checkSimMatches(t, step, s.win, host)
			}
			frame("empty")
			_, _ = a.emu.Write([]byte(symbolText))
			frame("symbols")
			m.moveFocused(1, 0) // everything shifts: cells redrawn at new places
			frame("moved right")
			m.moveFocused(0, 1)
			frame("moved down")
			_, _ = a.emu.Write([]byte("\x1b[1;4Hchanged")) // a neighbor of the symbols changes
			frame("partial update")
		})
	}
}

func checkSimMatches(t *testing.T, step string, scr *uv.Window, host *simTerm) {
	t.Helper()
	for y := range host.h {
		for x := 0; x < host.w; x++ {
			c := scr.CellAt(x, y)
			if c == nil || c.IsZero() {
				continue
			}
			// What the terminal is sent for the cell; where that is of
			// uncertain width, only its neighbors' places are checked.
			want := uv.SafeContent(c.Content, c.Width)
			if uv.UncertainWidth(want) {
				continue
			}
			if got := host.cells[y][x]; got != want {
				t.Fatalf("%s: (%d,%d) host has %q, goat drew %q\nhost:\n%s\ngoat:\n%s",
					step, x, y, got, want, host.dump(), dumpWin(scr))
			}
		}
	}
}

func (t *simTerm) dump() string {
	var b strings.Builder
	for _, row := range t.cells {
		b.WriteString(strings.Join(row, ""))
		b.WriteByte('\n')
	}
	return b.String()
}

func dumpWin(scr *uv.Window) string {
	var b strings.Builder
	for y := range scr.Height() {
		for x := 0; x < scr.Width(); x++ {
			c := scr.CellAt(x, y)
			if c == nil || c.IsZero() {
				continue
			}
			b.WriteString(c.Content)
		}
		b.WriteByte('\n')
	}
	return fmt.Sprint(b.String())
}

// hostileOutput is random program output meant to break things: text in
// many scripts, emoji sequences, combining marks, invisible and bidi
// characters, C0 and C1 controls, broken UTF-8, and escape sequences that
// move the cursor, scroll, erase, set modes and hyperlinks.
func hostileOutput(r *rand.Rand, n int) []byte {
	pieces := []string{
		"plain text ", "\r\n", "\n", "\r", "\t", "\b", "\a", "\x00", "\x7f", "\x0e", "\x0f",
		"é", "é", "à́̂", "Ж", "Ω", "中文", "ｶﾀｶﾅ", "한", "각",
		"ไทย", "ำ", "नमस्ते", "क्षि", "עברית", "عربي", "‮", "‭", "⁦", "⁩", "‎", "‏", "؜",
		"​", "‌", "‍", "\ufeff", "­", " ", " ", "￹", "͏",
		"⚙️", "⚙", "⚙︎", "👍🏽", "👨‍👩‍👧", "🇺🇸", "🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f", "1️⃣", "✔", "■", "↩", "ℹ️",
		"", "", "\U000F0001", "─│┼", "⠋",
		"\xc2\x85", "\xc2\x9b31m", "\xc2\x9d0;x\xc2\x9c", "\xff", "\xe2\x82", "\xf0\x9f",
		"\x1b[H", "\x1b[2J", "\x1b[K", "\x1b[1K", "\x1b[3;5H", "\x1b[99;99H", "\x1b[5A", "\x1b[40C",
		"\x1b[2;4r", "\x1b[r", "\x1b[3L", "\x1b[2M", "\x1b[4@", "\x1b[3P", "\x1b[5X", "\x1b[3S", "\x1b[2T",
		"\x1b[?7l", "\x1b[?7h", "\x1b[?6h", "\x1b[?6l", "\x1b[4h", "\x1b[4l", "\x1b[?1049h", "\x1b[?1049l", "\x1b[?3h",
		"\x1b[1;31;48;5;200m", "\x1b[0m", "\x1b[7m", "\x1b[38;2;1;2;3m", "\x1b#8", "\x1b(0lqk\x1b(B", "\x1bc",
		"\x1b]8;;http://x/\x1b\\link\x1b]8;;\x1b\\", "\x1b]8;id=\x07;http://a\x1b[2J/\x07L\x1b]8;;\x07",
		"\x1b]0;title\x07", "\x1b]2;t\x1b[31m\x07", "\x1bP+q544e\x1b\\", "\x1b[6n", "\x1b[c", "\x1b7", "\x1b8",
		"\x1b[18t", "\x1b[8;5;5t", "\x1b[?2026h", "\x1b[?2026l", "\x1b[3J", "\x1bD", "\x1bM", "\x1bE", "\x1bH",
		"\x1b[100b", "\x1b[s", "\x1b[u", "\x1b[?25l", "\x1b[ q",
	}
	var b bytes.Buffer
	for range n {
		b.WriteString(pieces[r.IntN(len(pieces))])
	}
	return b.Bytes()
}

// Host width models for the fuzz test: each disagrees with goat about every
// character goat isn't sure of, at random but consistently per character.
func hostileWidth(seed uint64) func(string) int {
	return func(s string) int {
		if !uv.UncertainWidth(s) {
			return uniseg.StringWidth(s)
		}
		if r, _ := utf8.DecodeRuneInString(s); r < 0x80 { // spaces for a cleaned-out cell
			return uniseg.StringWidth(s)
		}
		h := seed
		for _, c := range []byte(s) {
			h = h*1099511628211 ^ uint64(c)
		}
		return 1 + int(h>>7)%2
	}
}

// Whatever the programs print, the host terminal shows goat's frames and
// labels where goat drew them.
func TestHostileOutputStaysInWindows(t *testing.T) {
	for seed := range uint64(100) {
		r := rand.New(rand.NewPCG(seed, 7))
		m := newWM(DefaultConfig(), nil, 50, 18)
		bg := fakeWindow(0, 0, 0, 50, 17, false, false)
		bg.background = true
		bg.emu.Resize(bg.contentW(), bg.contentH())
		a := fakeWindow(1, 2, 1, 22, 10, true, false)
		b := fakeWindow(2, 14, 5, 24, 10, true, false)
		m.windows, m.bg = []*Window{bg, a, b}, bg
		m.focus(b)
		for _, w := range m.windows { // replies to queries go nowhere
			go func() { _, _ = io.Copy(io.Discard, w.emu) }()
		}

		var out bytes.Buffer
		s := newHostScreen(&out, []string{"TERM=xterm-256color"}, 50, 18)
		host := newSimTerm(50, 18, hostileWidth(seed))
		for step := range 12 {
			for _, w := range []*Window{bg, a, b} {
				_, _ = w.emu.Write(hostileOutput(r, 30))
			}
			if r.IntN(3) == 0 {
				m.moveFocused(r.IntN(5)-2, r.IntN(3)-1)
			}
			if r.IntN(4) == 0 {
				m.focus([]*Window{a, b}[r.IntN(2)])
			}
			out.Reset()
			if err := s.render(scene{m}, cursorState{}); err != nil {
				t.Fatal(err)
			}
			host.write(t, out.Bytes())
			if host.bad != "" {
				t.Fatalf("seed %d step %d: %s", seed, step, host.bad)
			}
			checkSimMatches(t, fmt.Sprintf("seed %d step %d", seed, step), s.win, host)
		}
	}
}

// garbage is what another program might write to the terminal behind
// goat's back: text, a switch to the line-drawing character set, a scroll
// region, insert or origin mode, no autowrap, colors, erasing.
func garbage(r *rand.Rand, n int) []byte {
	pieces := []string{
		"oops ", "Connection to host closed.\r\n", "\r\n", "中文", "⚙️", "\x1b(0", "\x1b)0\x0e", "\x0f",
		"\x1b[3;9r", "\x1b[?6h", "\x1b[4h", "\x1b[?7l", "\x1b[2J", "\x1b[K", "\x1b[31;44m", "\x1b[5;20H",
		"\x1b[12;3H", "lqqk", "\x1b[2L", "\x1b[3M", "\x1b[S",
	}
	var b bytes.Buffer
	for range n {
		b.WriteString(pieces[r.IntN(len(pieces))])
	}
	return b.Bytes()
}

// After anything at all has happened to the terminal, a repaint (Alt+r,
// focus back, repaint_seconds) puts every cell back.
func TestRepaintRepairsScreen(t *testing.T) {
	for seed := range uint64(30) {
		r := rand.New(rand.NewPCG(seed, 11))
		m, _, a, b := wmWithBackground(t)
		m.cfg.RepaintSeconds = 5
		for _, w := range m.windows {
			go func() { _, _ = io.Copy(io.Discard, w.emu) }()
		}
		_, _ = a.emu.Write([]byte("hello ⚙️ ✔ 中文\r\nsecond line"))
		_, _ = b.emu.Write(hostileOutput(r, 20))

		var out bytes.Buffer
		s := newHostScreen(&out, []string{"TERM=xterm-256color"}, 40, 12)
		host := newSimTerm(40, 12, hostileWidth(seed))
		frame := func() {
			t.Helper()
			out.Reset()
			if err := m.render(s); err != nil {
				t.Fatal(err)
			}
			host.write(t, out.Bytes())
			if host.bad != "" {
				t.Fatalf("seed %d: %s", seed, host.bad)
			}
		}
		frame()
		checkSimMatches(t, "before", s.win, host)

		for i, how := range []func(){
			func() { _ = m.handleKey(uv.Key{Code: 'r', Mod: uv.ModAlt}) },
			func() { _ = m.handleEvent(uv.FocusEvent{}, s) },
			func() { m.tick(m.lastRepaint.Add(5 * time.Second)) },
		} {
			host.inject(t, garbage(r, 12))
			_, _ = a.emu.Write([]byte("\r\nmore " + fmt.Sprint(i)))
			how()
			if !m.repaintHost {
				t.Fatalf("seed %d: way %d didn't ask for a repaint", seed, i)
			}
			frame()
			checkSimMatches(t, fmt.Sprintf("seed %d: after repaint %d", seed, i), s.win, host)
			if host.g != [2]byte{'B', host.g[1]} && host.g[0] != 0 || host.shifted || host.insert || host.origin || !host.autowrap || host.bot != 0 {
				t.Fatalf("seed %d: terminal state not reset: %+v", seed, host)
			}
		}
	}
}
