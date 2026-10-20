//go:build linux

package main

import (
	"testing"
	"time"
)

// A program that set keyboard modes and was killed leaves them; goat
// drops them once the shell is back in front.
func TestOrphanKeyboardDropped(t *testing.T) {
	m, _, _, _ := wmWithBackground(t)
	w, err := newWindow(1, "/bin/sh -i", 0, 0, 60, 10, winOpts{}, m.out)
	if err != nil {
		t.Fatal(err)
	}
	m.hookEvents(w)
	m.windows = []*Window{w}
	defer m.closeAll()
	w.send([]byte("python3 -c 'import os,sys,time; sys.stdout.write(\"\\033[>5u\"); sys.stdout.flush(); time.sleep(0.5); os.kill(os.getpid(), 9)'\n"))
	pump(t, m, func() bool { return len(w.kbd.stacks[0]) > 0 })
	if w.kbd.owner <= 0 || w.kbd.owner == w.pid() {
		t.Fatalf("owner %d (shell %d)", w.kbd.owner, w.pid())
	}
	if w.dropOrphanKeyboard() {
		t.Fatal("dropped while the program runs")
	}
	end := time.Now().Add(5 * time.Second)
	for !w.dropOrphanKeyboard() {
		if time.Now().After(end) {
			t.Fatalf("not dropped: flags %v", w.kbd.stacks[0])
		}
		time.Sleep(50 * time.Millisecond)
	}
	if w.kittyFlags() != 0 {
		t.Fatal("flags left")
	}
}
