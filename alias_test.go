package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The example from the request, plus a few more.
const aliasExample = `# ~/.config/goat/alias.toml

[host]
"long name" = "short name"
"build-*.corp.example.com" = "build"
"BUILD-07.corp.example.com" = "seven"

[directory]
"one name" = "another name"
"/user/opc/home" = "🏠"
"/user/opc/home/src/github.com" = "gh"
"~/notes" = "📝"
"/srv/*/logs" = "logs"

[repo]
"aaaaaaaaaaaa" = "aa"
"nontechno/goat" = "🐐"
"acme/*" = "acme"

[branch]
"*/main" = "🏡"
"aaaaaaaaaaaa/fdfsfsdfsdfs" = "monday"
"release/*" = "rel"
`

func TestGlobMatch(t *testing.T) {
	for _, tc := range []struct {
		p, s string
		fold bool
		want bool
	}{
		{"*/main", "nontechno/goat/main", false, true}, // * crosses "/"
		{"*/main", "main", false, false},
		{"a?c", "abc", false, true},
		{"a?c", "abbc", false, false},
		{"*", "", false, true},
		{"build-*.corp", "BUILD-7.corp", true, true},
		{"build-*.corp", "BUILD-7.corp", false, false},
		{"*a*b*", "xxaxxbxx", false, true},
		{"*a*b", "xxaxxbxxc", false, false},
	} {
		if got := globMatch(tc.p, tc.s, tc.fold); got != tc.want {
			t.Errorf("%q ~ %q (fold %v): %v", tc.p, tc.s, tc.fold, got)
		}
	}
}

func TestAliases(t *testing.T) {
	a, err := parseAliases(aliasExample, "/home/me")
	if err != nil {
		t.Fatal(err)
	}
	check := func(what, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s: %q, want %q", what, got, want)
		}
	}
	// Hosts: exact (any case) before patterns; others unchanged.
	check("host exact", a.host("long name"), "short name")
	check("host case", a.host("LONG NAME"), "short name")
	check("host exact wins over pattern", a.host("build-07.corp.example.com"), "seven")
	check("host pattern", a.host("build-12.corp.example.com"), "build")
	check("host none", a.host("other"), "other")
	check("host empty", a.host(""), "")

	// Directories: the longest covering key, replacing only its part.
	dir := func(p string) string {
		if d, ok := a.dir(p); ok {
			return d
		}
		return "(none)"
	}
	check("dir exact", dir("/user/opc/home"), "🏠")
	check("dir below", dir("/user/opc/home/src/goat"), "🏠/src/goat")
	check("dir longest", dir("/user/opc/home/src/github.com/nontechno/goat"), "gh/nontechno/goat")
	check("dir not a prefix of a name", dir("/user/opc/homework"), "(none)")
	check("dir ~", dir("/home/me/notes/today"), "📝/today")
	check("dir pattern", dir("/srv/app/logs"), "logs")
	check("dir none", dir("/etc"), "(none)")

	// Repositories: by owner/name, else by the name.
	check("repo full", a.repo("nontechno/goat"), "🐐")
	check("repo by name", a.repo("someone/aaaaaaaaaaaa"), "aa")
	check("repo bare name", a.repo("aaaaaaaaaaaa"), "aa")
	check("repo pattern", a.repo("acme/rockets"), "acme")
	check("repo none", a.repo("other/thing"), "other/thing")

	// Branches: repo/branch (full or by name), then the branch alone.
	check("branch */main", a.branch("nontechno/goat", "main"), "🏡")
	check("branch by repo name", a.branch("someone/aaaaaaaaaaaa", "fdfsfsdfsdfs"), "monday")
	check("branch bare repo", a.branch("aaaaaaaaaaaa", "fdfsfsdfsdfs"), "monday")
	check("branch other repo", a.branch("x/y", "fdfsfsdfsdfs"), "fdfsfsdfsdfs")
	check("branch alone", a.branch("x/y", "release/2.0"), "rel")
	check("branch none", a.branch("x/y", "dev"), "dev")
}

func TestAliasFileMistakes(t *testing.T) {
	for text, want := range map[string]string{
		"[hosts]\n\"a\" = \"b\"\n": "unknown section [hosts]",
		"[host]\n\"a\" = 1\n":      "incompatible types",
		"[host\n":                  "expected",
	} {
		if _, err := parseAliases(text, ""); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", text, err, want)
		}
	}
	// Control characters can't get into what is shown.
	a, err := parseAliases("[host]\n\"x\" = \"a\\u001b[31mb\"\n", "")
	if err != nil || strings.ContainsRune(a.host("x"), 0x1b) {
		t.Errorf("escape in an alias: %q %v", a.host("x"), err)
	}
}

// Aliases show in frames, the status bar, the git label and events, and
// follow alias.toml as it changes.
func TestAliasesShown(t *testing.T) {
	m, _, a, _ := wmWithBackground(t)
	m.cols = 100
	m.home = "/user/opc/home"
	m.host, m.user = "long name", "alice"
	m.cfg.StatusShowDir = true
	m.bar = barRight{clock: true, host: "long name"}
	path := filepath.Join(t.TempDir(), "alias.toml")
	m.alias.path = path
	if err := os.WriteFile(path, []byte(aliasExample), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if !m.loadAliases(now) {
		t.Fatal("not loaded")
	}

	a.w = 70
	m.focus(a) // (refreshes the window's directory and git: set them after)
	a.dir = "/user/opc/home/src/goat"
	a.git = gitInfo{repo: "nontechno/goat", branch: "main"}
	if got := m.frameLabel(a); got != userSymbol+"alice "+hostSymbol+"short name "+repoSymbol+"🐐 "+branchSymbol+"🏡" {
		t.Errorf("frame label %q", got)
	}
	line := statusLine(m)
	if !strings.Contains(line, hostSymbol+"short name") || !strings.Contains(line, "🏠/src/goat") {
		t.Errorf("status bar %q", line)
	}

	// Changed on disk: re-read within seconds.
	later := now.Add(time.Second)
	_ = os.WriteFile(path, []byte("[host]\n\"long name\" = \"tiny\"\n"), 0o644)
	_ = os.Chtimes(path, later, later)
	if m.refreshAliases(now.Add(time.Second)) {
		t.Error("re-read before the interval")
	}
	if !m.refreshAliases(now.Add(aliasRecheckEvery)) || m.showHost("long name") != "tiny" {
		t.Fatalf("not re-read: %q", m.showHost("long name"))
	}
	if m.showDir("/user/opc/home/src") != "~/src" {
		t.Errorf("directory alias gone, ~ expected: %q", m.showDir("/user/opc/home/src"))
	}

	// A mistake: listed once, aliases off until fixed.
	evs := len(m.ev.list)
	later = later.Add(time.Second)
	_ = os.WriteFile(path, []byte("[nope]\n"), 0o644)
	_ = os.Chtimes(path, later, later)
	m.refreshAliases(now.Add(2 * aliasRecheckEvery))
	if len(m.ev.list) != evs+1 || !strings.Contains(m.ev.list[len(m.ev.list)-1].text, "unknown section") {
		t.Errorf("mistake not listed: %v", m.ev.list[evs:])
	}
	if m.showHost("long name") != "long name" {
		t.Error("aliases used from a file with a mistake")
	}
	// Removed: nothing aliased.
	_ = os.Remove(path)
	m.refreshAliases(now.Add(3 * aliasRecheckEvery))
	if m.showHost("long name") != "long name" {
		t.Error("aliases kept after the file went")
	}
}

// Directory aliases replace a leading part of the path, at folder
// boundaries; the longest (most specific) key wins.
func TestDirectoryPrefixAliases(t *testing.T) {
	a, err := parseAliases(`[directory]
"/user/opc/home" = "🏠"
"/user/opc/home/abroad/villa" = "castle"
`, "")
	if err != nil {
		t.Fatal(err)
	}
	show := func(p string) string {
		if d, ok := a.dir(p); ok {
			return d
		}
		return p
	}
	for in, want := range map[string]string{
		"/user":                            "/user",
		"/user/opc":                        "/user/opc",
		"/user/opc/home":                   "🏠",
		"/user/opc/homestread":             "/user/opc/homestread",
		"/user/opc/home/garage":            "🏠/garage",
		"/user/opc/home/abroad":            "🏠/abroad",
		"/user/opc/home/abroad/villa":      "castle",
		"/user/opc/home/abroad/villa/more": "castle/more",
		"/user/opc/home/abroad/villas":     "🏠/abroad/villas",
	} {
		if got := show(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
	// The order in the file doesn't matter: still the longest key.
	b, _ := parseAliases(`[directory]
"/user/opc/home/abroad/villa" = "castle"
"/user/opc/home" = "🏠"
`, "")
	if d, _ := b.dir("/user/opc/home/abroad/villa/more"); d != "castle/more" {
		t.Errorf("order mattered: %q", d)
	}
	// A key written with a trailing slash means the same folder.
	c, _ := parseAliases(`[directory]
"/user/opc/home/" = "🏠"
`, "")
	if d, _ := c.dir("/user/opc/home/garage"); d != "🏠/garage" {
		t.Errorf("trailing slash: %q", d)
	}
}
