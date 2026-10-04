package main

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestSwapWithBackground(t *testing.T) {
	m, bg, a, b := wmWithBackground(t) // b (id 2) is focused, at 8,3 20x6
	_, _ = bg.emu.Write([]byte("BACKGROUND-APP"))
	_, _ = b.emu.Write([]byte("FLOATING-APP"))
	b.bgColor, b.fgColor, b.colorIndex = ansi.IndexedColor(17), ansi.IndexedColor(230), 2
	b.setWrap(false)
	b.shellName, bg.shellName = "vim", "bash"
	b.pinned = true
	x, y, w, h := b.x, b.y, b.w, b.h

	alt := func(r rune) { _ = m.handleKey(uv.Key{Code: r, Text: string(r), Mod: uv.ModAlt}) }
	alt('s')

	// b's program is now the background; the former background floats in
	// b's place, with b's number, and has the focus.
	if m.bg != b || !b.background || bg.background {
		t.Fatalf("roles: m.bg==b %v, b.background %v, old bg.background %v", m.bg == b, b.background, bg.background)
	}
	if b.x != 0 || b.y != 0 || b.w != m.cols || b.h != m.areaH() {
		t.Fatalf("new background geometry %d,%d %dx%d", b.x, b.y, b.w, b.h)
	}
	if bg.x != x || bg.y != y || bg.w != w || bg.h != h || !bg.pinned || b.pinned {
		t.Fatalf("new floating geometry %d,%d %dx%d pinned %v", bg.x, bg.y, bg.w, bg.h, bg.pinned)
	}
	if b.id != 0 || bg.id != 2 || m.number(b) != 0 || m.number(bg) != 2 {
		t.Fatalf("numbers: b=%d old bg=%d", b.id, bg.id)
	}
	if m.windows[0] != b || m.windows[2] != bg || m.focused != bg {
		t.Fatalf("order %v, focused %d", ids(m.windows), m.focused.id)
	}
	// Each program keeps its own state, and its terminal fits its new place.
	if b.bgColor != ansi.IndexedColor(17) || b.colorIndex != 2 || !b.nowrap || bg.nowrap {
		t.Fatal("colors / wrap did not stay with the program")
	}
	for _, win := range []*Window{b, bg} {
		if win.emu.Height() != win.contentH() || win.emu.Width() != win.emuWidth(win.contentW()) {
			t.Fatalf("window %d: emulator %dx%d, content %dx%d", win.id,
				win.emu.Width(), win.emu.Height(), win.contentW(), win.contentH())
		}
	}
	rows := render(m)
	if !strings.Contains(rows[0], "FLOATING-APP") {
		t.Fatalf("background row 0 %q", rows[0])
	}
	if !strings.Contains(rows[bg.contentY()], "BACKGROUND-APP") {
		t.Fatalf("floating window row %q", rows[bg.contentY()])
	}
	if status := rows[m.rows-1]; !strings.Contains(status, "0:vim") || !strings.Contains(status, "2:bash") {
		t.Fatalf("tabs not updated: %q", status)
	}

	// Swapping again restores the original arrangement.
	alt('s')
	if m.bg != bg || !bg.background || b.background || m.focused != b ||
		b.x != x || b.y != y || b.w != w || b.h != h || bg.id != 0 || b.id != 2 {
		t.Fatalf("second swap: bg=%v focused=%d b at %d,%d %dx%d", m.bg == bg, m.focused.id, b.x, b.y, b.w, b.h)
	}
	_ = a
}

func TestSwapRefused(t *testing.T) {
	m, bg, _, _ := wmWithBackground(t)
	m.focus(bg)
	_ = m.handleKey(uv.Key{Code: 's', Text: "s", Mod: uv.ModAlt})
	if m.bg != bg || !bg.background || !strings.Contains(m.status, "focus one first") {
		t.Fatalf("swap from the background: status %q", m.status)
	}
	m.toggleEvents() // the events window can't be swapped either
	if ew := m.ev.win; ew != nil && m.focused == ew {
		_ = m.handleKey(uv.Key{Code: 's', Text: "s", Mod: uv.ModAlt})
		if m.bg != bg || ew.background {
			t.Fatal("events window was swapped")
		}
	}
}
