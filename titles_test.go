package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestClassifyTitle(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  titleClass
	}{
		{"[ . ] Action Required ", titleAction},
		{"[ ! ] Action Required approve the edit", titleAction},
		{"[!] Action Required: deploy?", titleAction},
		{"[ ! ] Action Required", titleAction},
		{"[ ? ] Action Required ", titleOther},    // only . or !
		{"[ ! ] Action Requiredness", titleOther}, // a different word
		{"x [ ! ] Action Required ", titleOther},  // a prefix only
		{"⠋ Thinking…", titleSpinner},
		{"⠏ Working on it", titleSpinner},
		{"⠋Thinking", titleOther},  // no space after the frame
		{"⠂ Thinking", titleOther}, // not one of the frames
		{"vim main.go", titleOther},
		{"", titleOther},
	} {
		if got, _ := classifyTitle(tc.title); got != tc.want {
			t.Errorf("%q: %v, want %v", tc.title, got, tc.want)
		}
	}
}

// A run of action or spinner titles is listed once; any other title is
// listed as before and ends the run.
func TestTitleFilterSequence(t *testing.T) {
	var f titleFilter
	var got []string
	for _, title := range []string{
		"bash",
		"⠋ Thinking", "⠙ Thinking", "⠹ Thinking more", "⠸ Thinking", // one spinner event
		"[ ! ] Action Required approve", "[ . ] Action Required approve", // one action event
		"⠼ Working", "⠴ Working", // a spinner again (after the action)
		"claude",
		"⠦ Working", // after another title: listed again
		"[ . ] Action Required ",
		"[ ! ] Action Required ",
		"bash",
	} {
		if kind, text, ok := f.next(title); ok {
			got = append(got, string(kind)+" "+text)
		}
	}
	want := []string{
		`title "bash"`,
		`spinner "Thinking"`,
		`action "[ ! ] Action Required approve"`,
		`spinner "Working"`,
		`title "claude"`,
		`spinner "Working"`,
		`action "[ . ] Action Required"`,
		`title "bash"`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Through a real program: it sets titles with OSC 2; the events window
// lists the filtered events, and the title bar still follows every title.
func TestTitleEventsFromProgram(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("needs /bin/sh")
	}
	m := newWM(DefaultConfig(), nil, 80, 24)
	var b strings.Builder
	for _, title := range []string{"build", "⠋ Compiling", "⠙ Compiling", "⠹ Compiling",
		"[ ! ] Action Required ", "[ . ] Action Required ", "done", "⠼ Linking"} {
		b.WriteString(`printf '\033]2;` + title + `\007'; `)
	}
	path := t.TempDir() + "/run.sh"
	if err := os.WriteFile(path, []byte(b.String()+"sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := newWindow(1, "/bin/sh "+path, 0, 0, 40, 10, winOpts{}, m.out)
	if err != nil {
		t.Fatal(err)
	}
	m.hookEvents(w)
	m.windows = []*Window{w}
	defer m.closeAll()
	for end := time.Now().Add(5 * time.Second); w.oscTitle != "⠼ Linking" && time.Now().Before(end); {
		select {
		case msg := <-m.out:
			m.handlePty(msg)
		case <-time.After(50 * time.Millisecond):
		}
	}
	var got []string
	for _, e := range m.ev.list {
		got = append(got, string(e.kind)+" "+e.text)
	}
	want := `title "build"|spinner "Compiling"|action "[ ! ] Action Required"|title "done"|spinner "Linking"`
	if strings.Join(got, "|") != want {
		t.Fatalf("events:\n%s", strings.Join(got, "\n"))
	}
	if w.displayTitle("terminal") != "⠼ Linking" {
		t.Fatalf("title bar: %q", w.displayTitle("terminal"))
	}
}
