package main

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// Process figures for the status (F2) and inventory (F3) windows: memory
// and CPU of goat and of the programs in its windows.
//
// A sample is a table of every process on the machine (pid, parent, memory,
// CPU); a window's figures add up its shell and everything started under
// it. Sampling runs in the background, only while one of the two windows
// is on screen, at most every procEvery: from /proc on Linux, from ps(1)
// elsewhere (procinfo_linux.go, procinfo_other.go).

// procEvery is how often processes are sampled while they are shown.
const procEvery = 2 * time.Second

// procSample is one process.
type procSample struct {
	pid, ppid int
	rss       uint64  // resident memory, bytes
	cpu       float64 // percent of one CPU, recently
	comm      string  // command name
}

// procTable is a sample of all processes.
type procTable struct {
	at    time.Time
	procs map[int]procSample
	err   string // why there is no sample ("" if there is one)
}

// procSampler takes samples in the background; the latest is in table.
type procSampler struct {
	table   atomic.Pointer[procTable]
	running atomic.Bool
	// sample takes a sample (sampleProcs; replaced in tests). Only the
	// sampling goroutine calls it, one at a time, so it may keep state.
	sample func() procTable
}

// request starts a sample in the background unless one is running or the
// latest is recent.
func (s *procSampler) request(now time.Time) {
	if t := s.table.Load(); t != nil && now.Sub(t.at) < procEvery {
		return
	}
	if !s.running.CompareAndSwap(false, true) {
		return
	}
	sample := s.sample
	if sample == nil {
		sample = newProcSampleFunc()
	}
	s.sample = sample
	go func() {
		defer s.running.Store(false)
		t := sample()
		t.at = time.Now()
		s.table.Store(&t)
	}()
}

// parsePS reads `ps -axo pid=,ppid=,rss=,%cpu=,comm=`: rss in KB; the
// command (macOS: its path) is the rest of the line and may hold spaces.
func parsePS(out string) procTable {
	t := procTable{procs: map[int]procSample{}}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		rss, err3 := strconv.ParseUint(f[2], 10, 64)
		cpu, err4 := strconv.ParseFloat(strings.Replace(f[3], ",", ".", 1), 64) // (a decimal comma in some locales)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		comm := strings.Join(f[4:], " ")
		if strings.HasPrefix(comm, "/") {
			comm = path.Base(comm)
		}
		t.procs[pid] = procSample{pid: pid, ppid: ppid, rss: rss * 1024, cpu: cpu, comm: strings.TrimPrefix(comm, "-")}
	}
	return t
}

// procTotal is a group of processes added up.
type procTotal struct {
	count int
	rss   uint64
	cpu   float64
	names []string // command names, parents first, each once
}

// tree adds up the process pid and all its descendants.
func (t *procTable) tree(pid int) (tot procTotal, ok bool) {
	if t == nil || pid <= 0 {
		return tot, false
	}
	if _, ok := t.procs[pid]; !ok {
		return tot, false
	}
	children := map[int][]int{}
	for _, p := range t.procs {
		if p.pid != p.ppid {
			children[p.ppid] = append(children[p.ppid], p.pid)
		}
	}
	queue := []int{pid}
	seen := map[int]bool{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		p := t.procs[id]
		tot.count++
		tot.rss += p.rss
		tot.cpu += p.cpu
		if p.comm != "" && !slices.Contains(tot.names, p.comm) {
			tot.names = append(tot.names, p.comm)
		}
		kids := children[id]
		slices.Sort(kids)
		queue = append(queue, kids...)
	}
	return tot, true
}

// cpuMeter measures goat's own CPU use between two readings.
type cpuMeter struct {
	at    time.Time
	used  time.Duration // user + system CPU time at at
	pct   float64       // over the last interval
	known bool
}

// read takes a reading; the percentage is over the time since the last
// one (at least a second apart, else the last percentage stays).
func (c *cpuMeter) read(now time.Time) (pct float64, total time.Duration, ok bool) {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0, 0, false
	}
	used := time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
	switch {
	case c.at.IsZero():
		c.at, c.used = now, used
	case now.Sub(c.at) >= time.Second:
		c.pct = 100 * float64(used-c.used) / float64(now.Sub(c.at))
		c.at, c.used, c.known = now, used, true
	}
	return c.pct, used, c.known
}

// fmtBytes is a size for people: "812 KB", "41.2 MB", "1.3 GB".
func fmtBytes(n uint64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%d KB", n/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(k*k*k))
}

// fmtPct is a CPU percentage: "0.3%", "12%".
func fmtPct(p float64) string {
	if p < 10 {
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}

// fmtSince is a duration for people: "45s", "12m 5s", "3h 07m", "2d 4h".
func fmtSince(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

// fmtCount is a number with thousands separators: "12,345".
func fmtCount(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return "-" + fmtCount(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// nouns is a count and the noun for it: "1 process", "3 processes".
func nouns(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmtCount(n) + " " + many
}
