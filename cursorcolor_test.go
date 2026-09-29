package main

import (
	"bytes"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

type blankScene struct{}

func (blankScene) Draw(uv.Screen, uv.Rectangle) {}

func TestCursorColorFollowsWindowColors(t *testing.T) {
	var out bytes.Buffer
	s := newHostScreen(&out, nil, 20, 5)
	frame := func(c cursorState) string {
		t.Helper()
		out.Reset()
		if err := s.render(blankScene{}, c); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}
	cream := ansi.IndexedColor(230) // #ffffd7
	cur := cursorState{visible: true, x: 1, y: 1, shape: uv.CursorBlock, blink: true}

	if got := frame(cur); strings.Contains(got, "\x1b]12;") || strings.Contains(got, ansi.ResetCursorColor) {
		t.Fatalf("default colors should leave the cursor color alone: %q", got)
	}
	cur.color = cream
	if got := frame(cur); !strings.Contains(got, ansi.SetCursorColor("#ffffd7")) {
		t.Fatalf("cursor color not set: %q", got)
	}
	if got := frame(cur); strings.Contains(got, "\x1b]12;") {
		t.Fatalf("unchanged color sent again: %q", got)
	}
	cur.color = nil // focus moved to a window without Alt+b colors
	if got := frame(cur); !strings.Contains(got, ansi.ResetCursorColor) {
		t.Fatalf("cursor color not reset: %q", got)
	}

	cur.color = cream
	frame(cur)
	out.Reset()
	s.teardown(false)
	if !strings.Contains(out.String(), ansi.ResetCursorColor) {
		t.Fatalf("teardown left the cursor colored: %q", out.String())
	}
	if hexColor(nil) != "" || hexColor(ansi.IndexedColor(17)) != "#00005f" {
		t.Fatal("hexColor")
	}
}
