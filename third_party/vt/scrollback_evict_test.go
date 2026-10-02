package vt

import (
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// GOAT PATCH: lines dropped from a full buffer go to the evict handler,
// oldest first, intact; Clear does not report them.
func TestScrollbackEvictHandler(t *testing.T) {
	sb := NewScrollback(3)
	var got []string
	next := 0
	sb.SetEvictHandler(func(n int, l uv.Line) {
		if n != next {
			t.Errorf("evicted line %q numbered %d, want %d", l.String(), n, next)
		}
		next++
		got = append(got, l.String())
	})
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		sb.Push(lineOf(s))
	}
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("evicted %q", got)
	}
	sb.SetMaxLines(1)
	if strings.Join(got, ",") != "a,b,c,d" || sb.Len() != 1 || sb.Line(0).String() != "e" {
		t.Fatalf("after shrink: evicted %q, left %d", got, sb.Len())
	}
	sb.Clear()
	if len(got) != 4 {
		t.Fatalf("Clear reported lines: %q", got)
	}
	// After a Clear, numbering continues: a..e were lines 0-4, so f is 5.
	next = 5
	for _, s := range []string{"f", "g", "h"} {
		sb.Push(lineOf(s))
	}
	if sb.Len() != 1 || len(got) != 6 || got[4] != "f" || got[5] != "g" {
		t.Fatalf("after clear: evicted %q", got)
	}
}

func lineOf(s string) uv.Line {
	l := uv.NewLine(len(s))
	for i, r := range s {
		l[i] = uv.Cell{Content: string(r), Width: 1}
	}
	return l
}
