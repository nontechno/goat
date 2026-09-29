package main

import (
	"fmt"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

func BenchmarkFrame(b *testing.B) {
	m := newWM(DefaultConfig(), nil, 200, 50)
	bg := fakeWindow(0, 0, 0, 200, 49, false, false)
	bg.background = true
	bg.emu.Resize(bg.contentW(), bg.contentH())
	w1 := fakeWindow(1, 10, 5, 100, 30, true, false)
	w2 := fakeWindow(2, 60, 15, 100, 30, true, true)
	m.windows, m.bg = []*Window{bg, w1, w2}, bg
	m.focus(w2)
	for _, w := range m.windows {
		for i := range 60 {
			fmt.Fprintf(w.emu, "\x1b[3%dmline %d some text here to fill the row a bit more\x1b[0m\r\n", i%8, i)
		}
	}
	scr := uv.NewScreen(200, 50)
	b.ReportAllocs()
	for b.Loop() {
		scr.Clear()
		scene{m}.Draw(scr, scr.Bounds())
	}
}
