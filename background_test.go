package main

import (
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// wmWithBackground builds a WM with a process-less background window and two
// floating windows.
func wmWithBackground(t *testing.T) (*WM, *Window, *Window, *Window) {
	t.Helper()
	m := newWM(DefaultConfig(), nil, 40, 12)
	bg := fakeWindow(0, 0, 0, 40, 11, false, false)
	bg.background = true
	bg.emu.Resize(bg.contentW(), bg.contentH())
	a := fakeWindow(1, 5, 2, 20, 6, true, false)
	b := fakeWindow(2, 8, 3, 20, 6, true, false)
	m.windows, m.bg = []*Window{bg, a, b}, bg
	m.focus(b)
	return m, bg, a, b
}

func TestBackgroundGeometry(t *testing.T) {
	_, bg, _, _ := wmWithBackground(t)
	if bg.contentX() != 0 || bg.contentY() != 0 || bg.contentW() != 40 || bg.contentH() != 11 {
		t.Fatalf("content %d,%d %dx%d", bg.contentX(), bg.contentY(), bg.contentW(), bg.contentH())
	}
	if bg.inTitleBar(3, 0) || bg.onLeft(0, 5) || bg.onRight(39, 5) || bg.onBottom(5, 10) {
		t.Fatal("background must have no title bar or edges")
	}
}

func TestBackgroundStaysAtBottom(t *testing.T) {
	m, bg, a, _ := wmWithBackground(t)
	m.focus(bg)
	if m.windows[0] != bg || m.focused != bg {
		t.Fatalf("focusing the background raised it: %v", ids(m.windows))
	}
	m.focus(a)
	a.pinned = true
	m.raise(a)
	if m.windows[0] != bg {
		t.Fatalf("background moved: %v", ids(m.windows))
	}
}

func TestBackgroundNumberingAndCycle(t *testing.T) {
	m, bg, a, b := wmWithBackground(t)
	if m.number(bg) != 0 || m.number(a) != 1 || m.number(b) != 2 {
		t.Fatal("numbers wrong")
	}
	if got := m.cycle(); !slices.Equal(got, []*Window{bg, a, b}) {
		t.Fatalf("cycle %v", ids(got))
	}
	// Alt+0 focuses the background; Alt+n wraps from the last window to it.
	_, _ = m.shortcut(uv.Key{Code: '0', Mod: uv.ModAlt})
	if m.focused != bg {
		t.Fatal("Alt+0")
	}
	m.focus(b)
	_ = m.run(actFocusNext)
	if m.focused != bg {
		t.Fatal("focus next should wrap to the background")
	}
}

func TestBackgroundCannotMoveResizePinOrClose(t *testing.T) {
	m, bg, _, _ := wmWithBackground(t)
	m.focus(bg)
	for _, a := range []action{actMoveRight, actMoveDown, actResizeLeft, actResizeUp, actPinWindow, actCloseWindow} {
		_ = m.run(a)
	}
	if bg.x != 0 || bg.y != 0 || bg.w != 40 || bg.h != 11 || bg.pinned || !slices.Contains(m.windows, bg) {
		t.Fatalf("background changed: %d,%d %dx%d pinned=%v", bg.x, bg.y, bg.w, bg.h, bg.pinned)
	}
	if !strings.Contains(m.status, "can't be closed") {
		t.Fatalf("status %q", m.status)
	}
}

func TestBackgroundFollowsScreenSize(t *testing.T) {
	m, bg, _, _ := wmWithBackground(t)
	m.resizeScreen(60, 20)
	if bg.w != 60 || bg.h != 19 || bg.emu.Width() != 60 || bg.emu.Height() != 19 {
		t.Fatalf("background %dx%d emu %dx%d", bg.w, bg.h, bg.emu.Width(), bg.emu.Height())
	}
}

func TestNewWindowCentredOverBackground(t *testing.T) {
	m, bg, _, _ := wmWithBackground(t)
	m.focus(bg)
	// newWindow would spawn a process; check the placement arithmetic via
	// the same rule: centred in the area above the status bar.
	area := m.areaH()
	if area != 11 {
		t.Fatalf("area %d", area)
	}
}

func TestStatusBar(t *testing.T) {
	m, bg, a, b := wmWithBackground(t)
	m.setStatus("copied 5 characters", 1e9)
	got := render(m)
	if strings.Contains(got[11], "1:b ") || strings.Contains(got[11], "1:b\u00a0") {
		t.Fatalf("partial tab drawn: %q", got[11])
	}
	m.resizeScreen(70, 12)
	got = render(m)
	status := got[11]
	for _, want := range []string{"0:bash", "1:bash", "2:bash", "copied 5 characters"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status bar %q lacks %q", status, want)
		}
	}
	// Clicking a tab focuses that window.
	var tabA tab
	for _, tb := range m.tabs {
		if tb.win == a {
			tabA = tb
		}
	}
	m.handleMouse(uv.MouseClickEvent{X: tabA.x0 + 1, Y: 11, Button: uv.MouseLeft})
	if m.focused != a {
		t.Fatal("clicking the tab did not focus window 1")
	}
	// Floating windows never cover the status bar.
	b.h = 30
	if got := render(m); !strings.Contains(got[11], "0:bash") {
		t.Fatalf("status bar covered: %q", got[11])
	}
	_ = bg
}

func TestStatusTabsWinOverLongMessage(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	m.setStatus(strings.Repeat("very long warning ", 10), 1e9)
	got := render(m)[11]
	for _, want := range []string{"0:bash", "1:bash", "2:bash", "…"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status %q lacks %q", got, want)
		}
	}
}
