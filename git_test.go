package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// mkfile writes a file, making its folders.
func mkfile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindGit(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "work", "checkout")
	mkfile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	mkfile(t, filepath.Join(repo, ".git", "config"), `[core]
	bare = false
[remote "upstream"]
	url = https://example.com/other/upstream-name.git
[remote "origin"]
	url = git@github.com:nontechno/goat.git
	fetch = +refs/heads/*:refs/remotes/origin/*
`)
	deep := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if g := findGit(deep, now); g.frameText(true) != "nontechno/goat(main)" || g.top != repo {
		t.Errorf("deep inside: %+v", g)
	}
	// Detached.
	mkfile(t, filepath.Join(repo, ".git", "HEAD"), "4f2a9c1e5b7d4e8a9c217d4e5f6a8b90aabbccdd\n")
	if g := findGit(repo, now); g.frameText(true) != "nontechno/goat(detached 4f2a9c1)" {
		t.Errorf("detached: %q", g.frameText(true))
	}
	// A worktree: .git is a file; HEAD is its own, config the main one's.
	mkfile(t, filepath.Join(repo, ".git", "worktrees", "wt", "HEAD"), "ref: refs/heads/feature/x\n")
	mkfile(t, filepath.Join(repo, ".git", "worktrees", "wt", "commondir"), "../..\n")
	wt := filepath.Join(root, "wt")
	mkfile(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(repo, ".git", "worktrees", "wt")+"\n")
	if g := findGit(wt, now); g.frameText(true) != "nontechno/goat(feature/x)" {
		t.Errorf("worktree: %q", g.frameText(true))
	}
	// No origin: the first remote; no remotes: the folder.
	other := filepath.Join(root, "other")
	mkfile(t, filepath.Join(other, ".git", "HEAD"), "ref: refs/heads/dev\n")
	mkfile(t, filepath.Join(other, ".git", "config"), "[remote \"up\"]\n\turl = /srv/git/thing.git\n")
	if g := findGit(other, now); g.frameText(true) != "thing(dev)" {
		t.Errorf("first remote: %q", g.frameText(true))
	}
	mkfile(t, filepath.Join(other, ".git", "config"), "[core]\n")
	if g := findGit(other, now); g.frameText(true) != "other(dev)" {
		t.Errorf("no remote: %q", g.frameText(true))
	}
	// Not a repository.
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if g := findGit(plain, now); g.frameText(false) != "" {
		t.Errorf("outside: %q", g.frameText(false))
	}
	// Control characters can't get through.
	mkfile(t, filepath.Join(other, ".git", "HEAD"), "ref: refs/heads/a\x1b[31mb\n")
	if g := findGit(other, now); strings.ContainsRune(g.label(), 0x1b) {
		t.Errorf("escape in label: %q", g.label())
	}
}

func TestRepoFromURL(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:nontechno/goat.git":                "nontechno/goat",
		"github.com:nontechno/goat":                        "nontechno/goat",
		"https://github.com/nontechno/goat":                "nontechno/goat",
		"https://github.com/nontechno/goat/":               "nontechno/goat",
		"https://user:token@github.com/nontechno/goat.git": "nontechno/goat",
		"ssh://git@host:22/team/project.git":               "team/project",
		"https://gitlab.com/group/sub/project.git":         "group/sub/project",
		"https://dev.azure.com/org/proj/_git/repo":         "org/proj/repo",
		"git@ssh.dev.azure.com:v3/org/proj/repo":           "v3/org/proj/repo",
		"https://example.com/onlyname.git":                 "onlyname",
		"/srv/git/thing.git":                               "thing",
		"../relative/thing":                                "thing",
		"file:///home/me/src/local-repo.git/":              "local-repo",
		`C:\repos\win.git`:                                 "win",
		"git@host:own\x1ber/na\u202eme":                    "owner/name",
	} {
		if got := repoFromURL(url); got != want {
			t.Errorf("%q: %q, want %q", url, got, want)
		}
	}
}

// Each window's repository and branch in its bottom border, after the host
// name; a branch switch is noticed (in any window) and listed in the events
// window; nothing while the window runs ssh, or with show_git = false.
func TestGitInFrame(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "goat-checkout")
	head := filepath.Join(repo, ".git", "HEAD")
	mkfile(t, head, "ref: refs/heads/main\n")
	mkfile(t, filepath.Join(repo, ".git", "config"), "[remote \"origin\"]\n\turl = https://github.com/nontechno/goat.git\n")

	m, _, a, b := wmWithBackground(t)
	m.cols = 80
	m.host, m.user = "box", "alice"
	m.cfg.ShowUser = false
	a.w, b.w = 46, 36 // room for the label
	a.oscDir = repo   // a is not the focused window
	now := time.Now()
	m.tick(now)
	border := func(w *Window) string {
		t.Helper()
		scr := uv.NewScreen(m.cols, m.rows)
		scene{m}.Draw(scr, scr.Bounds())
		var sb strings.Builder
		for x := w.x; x < w.x+w.w; x++ {
			if c := scr.CellAt(x, w.y+w.h-1); c != nil && !c.IsZero() {
				sb.WriteString(c.Content)
			}
		}
		return sb.String()
	}
	m.raise(a) // so its bottom border isn't covered
	if got := border(a); !strings.Contains(got, hostSymbol+"box "+repoSymbol+"nontechno/goat "+branchSymbol+"main") {
		t.Fatalf("frame %q", got)
	}
	if strings.Contains(border(b), repoSymbol) {
		t.Error("b isn't in a repository")
	}

	// git switch feature, in the window that isn't focused.
	mkfile(t, head, "ref: refs/heads/feature\n")
	later := now.Add(time.Second)
	_ = os.Chtimes(head, later, later)
	m.tick(later)
	if got := border(a); !strings.Contains(got, branchSymbol+"feature") {
		t.Fatalf("after the switch: %q", got)
	}
	var got []string
	for _, e := range m.ev.list {
		if e.kind == evGit {
			got = append(got, e.text)
		}
	}
	if strings.Join(got, "|") != "nontechno/goat: main → feature" {
		t.Errorf("events: %q", got)
	}

	// While ssh runs in the window: no git label.
	a.procName = "ssh"
	if strings.Contains(border(a), repoSymbol) {
		t.Error("shown during ssh")
	}
	a.procName = ""
	// Terminals that can't draw the emoji: plain.
	m.plainIdent = true
	if got := border(a); !strings.Contains(got, "box nontechno/goat(feature)") {
		t.Errorf("plain: %q", got)
	}
	m.plainIdent = false
	// Leaving the repository; turned off.
	a.oscDir = t.TempDir()
	m.tick(now.Add(2 * time.Second))
	if strings.Contains(border(a), repoSymbol) {
		t.Error("still shown outside the repository")
	}
	a.oscDir = repo
	m.tick(now.Add(3 * time.Second))
	m.cfg.ShowGit = false
	if strings.Contains(border(a), repoSymbol) {
		t.Error("shown with show_git = false")
	}
}
