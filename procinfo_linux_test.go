//go:build linux

package main

import (
	"os"
	"testing"
	"time"
)

func TestParseProcStat(t *testing.T) {
	// The command name may hold spaces and parentheses.
	s := "4242 (my (odd) prog) S 4000 4242 4242 34816 4242 4194304 100 0 0 0 150 50 0 0 20 0 1 0 12345 10000000 256 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 17 3 0 0 0 0 0\n"
	p, ticks, ok := parseProcStat(s, 4096)
	if !ok || p.pid != 4242 || p.ppid != 4000 || p.comm != "my (odd) prog" || ticks != 200 || p.rss != 256*4096 {
		t.Fatalf("%+v ticks %d ok %v", p, ticks, ok)
	}
	if _, _, ok := parseProcStat("garbage", 4096); ok {
		t.Error("garbage parsed")
	}
}

// The real sampler finds goat's test process, with its memory.
func TestSampleProcsLinux(t *testing.T) {
	sample := newProcSampleFunc()
	sample()
	time.Sleep(20 * time.Millisecond)
	tb := sample()
	p, ok := tb.procs[os.Getpid()]
	if !ok || p.rss == 0 || p.comm == "" {
		t.Fatalf("own process: %+v (%d processes, err %q)", p, len(tb.procs), tb.err)
	}
}

// macOS's kern.procargs2 (procname_darwin.go processArgs).
func TestParseProcArgs2(t *testing.T) {
	b := []byte{3, 0, 0, 0}
	b = append(b, "/usr/bin/ssh\x00\x00\x00\x00ssh\x00-p\x002222\x00HOME=/x\x00"...)
	got := parseProcArgs2(b)
	if len(got) != 3 || got[0] != "ssh" || got[1] != "-p" || got[2] != "2222" {
		t.Errorf("%q", got)
	}
	if parseProcArgs2([]byte{1, 0}) != nil {
		t.Error("short")
	}
}
