package main

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestParseStatus(t *testing.T) {
	long := func(n int) string { return strings.Repeat("A", n) }
	for _, tc := range []struct {
		body string
		want string // describe(""), or "error: ..." for a report ignored whole
	}{
		{"state=idle", "idle"},
		{"state=working:progress=40:app=claude", "working app=claude progress=40%"},
		{"state=working", "working progress=?"},
		{"state=blocked:kind=permission:msg=" + b64("Allow edit of main.go?"), `blocked kind=permission progress=? msg="Allow edit of main.go?"`},
		{"state=blocked:kind=unknownkind", "blocked progress=?"},
		{"state=done:kind=permission:progress=50", "done"}, // kind and progress only where they belong
		{"state=working:progress=101", "working progress=?"},
		{"state=working:progress=-1", "working progress=?"},
		{"state=working:progress=4x", "working progress=?"},
		{"state=error:id=deploy/us-east:title=" + strings.TrimRight(b64("us-east"), "="), `error id=deploy/us-east title="us-east"`},
		{"state=idle:app=bad/app", "idle"},     // app: no "/"
		{"state=idle:app=" + long(33), "idle"}, // too long: absent
		{"state=working:state=done", "done"},   // the last one counts
		{"state=idle:future=x:other=1", "idle ignored keys: future,other"},
		{"state=idle:Bad=1:nokey:x=a;b", "idle skipped 3 malformed pair(s)"},
		{"state=clear:id=a", "clear id=a"},
		// Ignored whole.
		{"", "error: no state"},
		{"progress=5", "error: no state"},
		{"state=paused", `error: unknown state "paused"`},
		{"state=idle:id=a//b", `error: invalid id "a//b"`},
		{"state=idle:id=" + long(33), "error: invalid id"},
		{"state=idle:id=a/b/c/d/e/f/g/h/i", "error: invalid id"},
		{"state=idle:msg=" + b64("two\nlines"), "error: msg: control characters"},
		{"state=idle:msg=" + b64("bell\x07"), "error: msg: control characters"},
		{"state=idle:msg=" + b64("c1 \u0085"), "error: msg: control characters"},
		{"state=idle:msg=!!!", "idle skipped 1 malformed pair(s)"}, // "!" isn't a value character
		{"state=idle:msg=AAAAA", "error: msg: invalid base64"},
		{"state=idle:title=" + b64(strings.Repeat("x", 193)), "error: title:"},
		{"state=idle:msg=" + long(2733), "error: msg: longer than 2732 bytes encoded"},
		{"state=idle:abcdefghijklmnopq=1", "error: key"},
		{"state=idle:x=" + long(4100), "error: longer than 4096 bytes"},
	} {
		r, query, err := parseStatus(tc.body)
		if query {
			t.Errorf("%q: taken for a query", tc.body)
			continue
		}
		got := ""
		if err != nil {
			got = "error: " + err.Error()
		} else {
			got = r.describe("")
		}
		if !strings.HasPrefix(got, tc.want) {
			t.Errorf("%.40q:\n got %s\nwant %s", tc.body, got, tc.want)
		}
	}
	if _, query, err := parseStatus("?"); !query || err != nil {
		t.Errorf("? is the query: %v %v", query, err)
	}
	// Padding optional.
	for _, enc := range []string{b64("hi"), strings.TrimRight(b64("hi"), "=")} {
		if r, _, err := parseStatus("state=idle:msg=" + enc); err != nil || r.msg != "hi" {
			t.Errorf("msg=%s: %q %v", enc, r.msg, err)
		}
	}
}

func TestStatusStore(t *testing.T) {
	var s statusStore
	rep := func(body string) statusReport {
		t.Helper()
		r, _, err := parseStatus(body)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	s.apply(rep("state=working:app=deploy"))
	s.apply(rep("state=working:id=deploy/eu"))
	s.apply(rep("state=blocked:id=deploy/us:kind=auth"))
	s.apply(rep("state=done:id=deployx"))
	if got := s.app("deploy/eu"); got != "deploy" {
		t.Errorf("inherited app %q", got)
	}
	// A report replaces its record whole.
	s.apply(rep("state=idle"))
	if s.records[""].app != "" || s.app("deploy/eu") != "" {
		t.Errorf("root record kept its app: %+v", s.records[""])
	}
	removed := s.apply(rep("state=clear:id=deploy"))
	if strings.Join(sortedStrings(removed), ",") != "deploy/eu,deploy/us" || len(s.records) != 2 {
		t.Errorf("clear deploy: removed %v, left %d", removed, len(s.records))
	}
	if removed := s.dropTransient(); len(removed) != 0 {
		t.Errorf("nothing transient left: %v", removed)
	}
	s.apply(rep("state=working:id=a"))
	if removed := s.dropTransient(); strings.Join(removed, ",") != "a" {
		t.Errorf("prompt: %v", removed)
	}
	if removed := s.apply(rep("state=clear")); len(removed) != 2 || len(s.records) != 0 {
		t.Errorf("clear all: %v", removed)
	}
	// Full: the least recently updated goes.
	for i := range statusMaxRecords + 1 {
		s.apply(rep("state=idle:id=r" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)))
	}
	if len(s.records) != statusMaxRecords {
		t.Errorf("%d records", len(s.records))
	}
	if _, ok := s.records["ra"]; ok {
		t.Error("the oldest record stayed")
	}
}

func sortedStrings(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// End to end, through a real program: the query is answered (before the
// device attributes asked for after it), reports are kept and listed in the
// events window, a prompt ends working/blocked, a reset clears everything.
func TestStatusProtocol(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	dir := t.TempDir()
	m := newWM(DefaultConfig(), nil, 80, 24)
	script := `stty raw -echo; printf '\033]7501;?\033\\\033[c'; head -c 10 > ` + dir + `/reply; ` +
		`printf '\033]7501;state=working:app=claude:progress=10\033\\'; ` +
		`printf '\033]7501;state=blocked:kind=permission:id=t1:msg=` + b64("Allow edit?") + `\007'; ` +
		`printf '\033]7501;state=bogus\007'; ` +
		`printf '\033]133;A\007'; ` +
		`printf '\033]7501;state=done:id=t2\007'; ` +
		`printf '\033c'; touch ` + dir + `/end; sleep 5`
	path := dir + "/run.sh"
	if err := os.WriteFile(path, []byte(script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := newWindow(1, "/bin/sh "+path, 0, 0, 40, 10, winOpts{}, m.out)
	if err != nil {
		t.Fatal(err)
	}
	m.hookEvents(w)
	m.windows = []*Window{w}
	defer m.closeAll()

	reset := false
	for end := time.Now().Add(5 * time.Second); !reset && time.Now().Before(end); {
		select {
		case msg := <-m.out:
			m.handlePty(msg)
		case <-time.After(50 * time.Millisecond):
		}
		for _, e := range m.ev.list {
			reset = reset || strings.HasPrefix(e.text, "terminal reset")
		}
	}
	reply, _ := os.ReadFile(dir + "/reply")
	if string(reply) != statusQueryReply {
		t.Errorf("reply %q, want %q", reply, statusQueryReply)
	}
	var got []string
	for _, e := range m.ev.list {
		if e.kind == evStatus {
			got = append(got, e.text)
		}
	}
	want := []string{
		"query ? (answered: supported)",
		"working app=claude progress=10%",
		`blocked id=t1 app=claude (inherited) kind=permission progress=? msg="Allow edit?"`,
		`ignored: unknown state "bogus": "state=bogus"`,
		"prompt: ended (root), t1", // in either order: sorted below
		"done id=t2",
		"terminal reset: cleared t2",
	}
	if len(got) == len(want) {
		got[4] = "prompt: ended " + strings.Join(sortedStrings(strings.Split(strings.TrimPrefix(got[4], "prompt: ended "), ", ")), ", ")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if len(w.status.records) != 0 {
		t.Errorf("records left after reset: %d", len(w.status.records))
	}
}
