package main

import (
	"fmt"
	"math/rand"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// The vendored ultraviolet (third_party/ultraviolet) rotates line slices
// when scrolling full-width regions instead of copying every cell. Check it
// against the original cell-by-cell algorithm.

func refInsert(lines [][]string, y, n int, area uv.Rectangle) {
	if n <= 0 || y < area.Min.Y || y >= area.Max.Y || y >= len(lines) {
		return
	}
	if y+n > area.Max.Y {
		n = area.Max.Y - y
	}
	for i := area.Max.Y - 1; i >= y+n; i-- {
		for x := area.Min.X; x < area.Max.X; x++ {
			lines[i][x] = lines[i-n][x]
		}
	}
	for i := y; i < y+n; i++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			lines[i][x] = "_"
		}
	}
}

func refDelete(lines [][]string, y, n int, area uv.Rectangle) {
	if n <= 0 || y < area.Min.Y || y >= area.Max.Y || y >= len(lines) {
		return
	}
	if n > area.Max.Y-y {
		n = area.Max.Y - y
	}
	for dst := y; dst < area.Max.Y-n; dst++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			lines[dst][x] = lines[dst+n][x]
		}
	}
	for i := area.Max.Y - n; i < area.Max.Y; i++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			lines[i][x] = "_"
		}
	}
}

func TestPatchedScrollMatchesOriginal(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const w, h = 7, 9
	blank := &uv.Cell{Content: "_", Width: 1}
	for iter := range 5000 {
		buf := uv.NewBuffer(w, h)
		ref := make([][]string, h)
		for y := range h {
			ref[y] = make([]string, w)
			for x := range w {
				s := fmt.Sprintf("%c", 'a'+rune((y*w+x)%26))
				buf.SetCell(x, y, &uv.Cell{Content: s, Width: 1})
				ref[y][x] = s
			}
		}
		for range 6 {
			top := rng.Intn(h)
			bot := top + 1 + rng.Intn(h-top)
			area := uv.Rect(0, top, w, bot-top) // full width: the patched path
			if rng.Intn(3) == 0 {               // sometimes partial width: original path
				l := rng.Intn(w - 1)
				area = uv.Rect(l, top, 1+rng.Intn(w-l), bot-top)
			}
			y := top + rng.Intn(bot-top)
			n := 1 + rng.Intn(h)
			if rng.Intn(2) == 0 {
				buf.InsertLineArea(y, n, blank, area)
				refInsert(ref, y, n, area)
			} else {
				buf.DeleteLineArea(y, n, blank, area)
				refDelete(ref, y, n, area)
			}
		}
		for y := range h {
			for x := range w {
				if got := buf.CellAt(x, y).Content; got != ref[y][x] {
					t.Fatalf("iteration %d: cell %d,%d = %q, want %q", iter, x, y, got, ref[y][x])
				}
			}
		}
		// Every line must still be its own slice (no aliasing after rotation).
		seen := map[*uv.Cell]bool{}
		for y := range h {
			p := &buf.Lines[y][0]
			if seen[p] {
				t.Fatalf("iteration %d: line %d shares storage with another line", iter, y)
			}
			seen[p] = true
		}
	}
}
