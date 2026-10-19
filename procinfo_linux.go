//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// clockTicks is USER_HZ, the unit of CPU times in /proc (100 on every
// Linux goat runs on).
const clockTicks = 100

// newProcSampleFunc returns a function that samples /proc: memory from
// /proc/<pid>/stat, CPU as the change in CPU time since its last sample.
func newProcSampleFunc() func() procTable {
	var prev map[int]uint64 // CPU ticks at the last sample
	var prevAt time.Time
	return func() procTable {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return procTable{err: err.Error()}
		}
		now := time.Now()
		page := uint64(os.Getpagesize())
		t := procTable{procs: map[int]procSample{}}
		ticks := map[int]uint64{}
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil {
				continue
			}
			b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
			if err != nil {
				continue // gone, or not ours to read
			}
			p, used, ok := parseProcStat(string(b), page)
			if !ok || p.pid != pid {
				continue
			}
			ticks[pid] = used
			if was, ok := prev[pid]; ok && used >= was && !prevAt.IsZero() {
				secs := now.Sub(prevAt).Seconds()
				if secs > 0 {
					p.cpu = 100 * float64(used-was) / clockTicks / secs
				}
			}
			t.procs[pid] = p
		}
		prev, prevAt = ticks, now
		return t
	}
}

// parseProcStat reads /proc/<pid>/stat: "pid (comm) state ppid ...".
// The command name may hold spaces and parentheses: it ends at the last ")".
func parseProcStat(s string, page uint64) (p procSample, cpuTicks uint64, ok bool) {
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return p, 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		return p, 0, false
	}
	f := strings.Fields(s[end+1:]) // f[0] is field 3 (state)
	if len(f) < 22 {
		return p, 0, false
	}
	ppid, _ := strconv.Atoi(f[1])
	utime, _ := strconv.ParseUint(f[11], 10, 64) // field 14
	stime, _ := strconv.ParseUint(f[12], 10, 64) // field 15
	rss, _ := strconv.ParseInt(f[21], 10, 64)    // field 24, pages
	p = procSample{pid: pid, ppid: ppid, comm: s[open+1 : end]}
	if rss > 0 {
		p.rss = uint64(rss) * page
	}
	return p, utime + stime, true
}

// memoryTotal is the machine's memory, from /proc/meminfo (0 if unknown).
func memoryTotal() uint64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			f := strings.Fields(rest) // "16303116 kB"
			if len(f) >= 1 {
				if kb, err := strconv.ParseUint(f[0], 10, 64); err == nil {
					return kb * 1024
				}
			}
		}
	}
	return 0
}
