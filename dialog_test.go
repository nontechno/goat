package main

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func altKey(r rune) uv.Key { return uv.Key{Code: r, Text: string(r), Mod: uv.ModAlt} }

// Only the background shell: Alt+q quits at once.
func TestQuitWithoutWindows(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	for _, w := range m.floatingWindows() {
		m.remove(w)
	}
	m.toggleEvents() // the events window doesn't count
	_ = m.handleKey(altKey('q'))
	if !m.quit || m.quitAsk != nil {
		t.Fatalf("quit %v, dialog %v", m.quit, m.quitAsk != nil)
	}
}

func TestQuitDialogKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []uv.Key
		quit bool
	}{
		{"Enter: Cancel is the default", []uv.Key{{Code: uv.KeyEnter}}, false},
		{"Esc", []uv.Key{{Code: uv.KeyEscape}}, false},
		{"n", []uv.Key{{Code: 'n', Text: "n"}}, false},
		{"y", []uv.Key{{Code: 'y', Text: "y"}}, true},
		{"Y", []uv.Key{{Code: 'y', Text: "Y", Mod: uv.ModShift}}, true},
		{"Alt+q again", []uv.Key{altKey('q')}, true},
		{"Right, Enter", []uv.Key{{Code: uv.KeyRight}, {Code: uv.KeyEnter}}, true},
		{"Tab twice, Enter", []uv.Key{{Code: uv.KeyTab}, {Code: uv.KeyTab}, {Code: uv.KeyEnter}}, false},
	} {
		m, _, _, w := wmWithBackground(t)
		_ = m.handleKey(altKey('q'))
		if m.quit || m.quitAsk == nil {
			t.Fatalf("%s: no dialog (quit %v)", tc.name, m.quit)
		}
		for _, k := range tc.keys {
			_ = m.handleKey(k)
		}
		if m.quit != tc.quit {
			t.Errorf("%s: quit = %v", tc.name, m.quit)
		}
		if !tc.quit && m.quitAsk != nil {
			t.Errorf("%s: dialog still up", tc.name)
		}
		if !tc.quit && m.focused != w {
			t.Errorf("%s: focus moved", tc.name)
		}
	}
}

// While the dialog is up, nothing reaches the windows, and shortcuts don't
// run.
func TestQuitDialogIsModal(t *testing.T) {
	m, _, _, w := wmWithBackground(t)
	n := len(m.windows)
	_ = m.handleKey(altKey('q'))
	_ = m.handleKey(altKey('c'))                  // new window: not now
	_ = m.handleKey(uv.Key{Code: 'x', Text: "x"}) // typing
	m.paste("rm -rf /\n")                         // pasting
	m.handleMouse(uv.MouseClickEvent{X: w.x + 1, Y: w.y + 1, Button: uv.MouseLeft})
	if len(m.windows) != n || m.quitAsk == nil || m.quit {
		t.Fatalf("windows %d -> %d, dialog %v, quit %v", n, len(m.windows), m.quitAsk != nil, m.quit)
	}
	if x, y, ok := m.cursorFor(); ok {
		t.Fatalf("cursor shown at %d,%d under the dialog", x, y)
	}
}

func TestQuitDialogDrawnAndClicked(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	_ = m.handleKey(altKey('q'))
	screen := strings.Join(render(m), "\n")
	for _, want := range []string{"Quit goat?", "2 windows are open", "  Quit  ", "> Cancel <", "y: quit"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
	d := m.quitAsk
	m.handleMouse(uv.MouseClickEvent{X: d.cancelBtn.Min.X + 1, Y: d.cancelBtn.Min.Y, Button: uv.MouseLeft})
	if m.quitAsk != nil || m.quit {
		t.Fatal("Cancel click")
	}
	_ = m.handleKey(altKey('q'))
	render(m)
	d = m.quitAsk
	m.handleMouse(uv.MouseClickEvent{X: d.quitBtn.Min.X, Y: d.quitBtn.Min.Y, Button: uv.MouseLeft})
	if !m.quit {
		t.Fatal("Quit click")
	}
}

// The selected button stands out beyond doubt: markers, a bright fill
// unlike the box and the other button, readable bold text; moving the
// selection moves all of that.
func TestQuitDialogSelectionIsClear(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	_ = m.handleKey(altKey('q'))
	box := m.cfg.Theme.StatusBg.C
	check := func(sel, other uv.Rectangle, selLabel, otherLabel string) {
		t.Helper()
		scr := uv.NewScreen(m.cols, m.rows)
		scene{m}.Draw(scr, scr.Bounds())
		text := func(r uv.Rectangle) string {
			var b strings.Builder
			for x := r.Min.X; x < r.Max.X; x++ {
				b.WriteString(scr.CellAt(x, r.Min.Y).Content)
			}
			return b.String()
		}
		if got := text(sel); got != "> "+selLabel+" <" {
			t.Fatalf("selected button reads %q", got)
		}
		if got := text(other); got != "  "+otherLabel+"  " {
			t.Fatalf("other button reads %q", got)
		}
		s := scr.CellAt(sel.Min.X+2, sel.Min.Y).Style
		o := scr.CellAt(other.Min.X+2, other.Min.Y).Style
		if s.Attrs&uv.AttrBold == 0 || o.Attrs&uv.AttrBold != 0 {
			t.Fatal("bold: selected only")
		}
		if cr := contrastRatio(luminance(s.Bg), luminance(box)); cr < 3 {
			t.Fatalf("selected fill vs the box: contrast %.2f", cr)
		}
		if cr := contrastRatio(luminance(s.Bg), luminance(o.Bg)); cr < 2 {
			t.Fatalf("selected vs other button fill: contrast %.2f", cr)
		}
		if cr := contrastRatio(luminance(s.Fg), luminance(s.Bg)); cr < 4.5 {
			t.Fatalf("selected label: contrast %.2f", cr)
		}
		if cr := contrastRatio(luminance(o.Fg), luminance(o.Bg)); cr < 3 {
			t.Fatalf("other label: contrast %.2f", cr)
		}
	}
	render(m)
	d := m.quitAsk
	check(d.cancelBtn, d.quitBtn, "Cancel", "Quit")
	_ = m.handleKey(uv.Key{Code: uv.KeyTab})
	check(d.quitBtn, d.cancelBtn, "Quit", "Cancel")
}
