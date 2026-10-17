package main

import (
	"bufio"
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Git in window frames (show_git): when a window's current directory is
// inside a git repository, its bottom border shows the repository name and
// the branch after the host name:
//
//	└─ 🧑alice 💻myhost 📖nontechno/goat 🌿main ────[]─┘
//
// or the commit when no branch is checked out ("🌿detached a1b2c3d"). Not
// while the window runs ssh: its directory is then the local one, not where
// the session is. The background shell has no frame, so no git label.
//
// goat doesn't run git. It looks for .git from the directory upwards (a
// directory, or a file pointing elsewhere: worktrees, submodules), reads
// HEAD for the branch, and takes "owner/name" from the "origin" remote's URL
// in the repository's config (else the first remote's; else the folder's
// name, without an owner).
// HEAD is checked twice a second (one stat), so a branch switch shows at
// once; it is also listed in the events window. The search is redone when
// the directory changes, and every gitRecheckEvery in case a repository
// appeared or went away.

const gitRecheckEvery = 5 * time.Second

// gitInfo is what a window's directory says about git.
type gitInfo struct {
	repo   string // "owner/name" (or "name"); "" = not in a repository
	branch string // "main", or "detached a1b2c3d"

	dir      string    // the directory this was found for
	top      string    // the working tree's top folder
	headPath string    // .git/HEAD (of the worktree)
	headMod  time.Time // HEAD as last read
	headSize int64
	found    time.Time // when the search was last done
}

// (repoSymbol, branchSymbol: iconography.go)

// label identifies the repository and branch ("" outside a repository).
func (g gitInfo) label() string {
	if g.repo == "" {
		return ""
	}
	return g.repo + "\x00" + g.branch
}

// frameText is the git label in a window frame: "📖nontechno/goat 🌿main"; plain
// (for terminals that can't draw the emoji, goat -learn): "goat(main)".
func (g gitInfo) frameText(plain bool) string {
	switch {
	case g.repo == "":
		return ""
	case plain && g.branch == "":
		return g.repo
	case plain:
		return g.repo + "(" + g.branch + ")"
	case g.branch == "":
		return repoSymbol + g.repo
	}
	return repoSymbol + g.repo + " " + branchSymbol + g.branch
}

// refreshGit brings w.git up to date with w.dir. changed reports whether the
// label changed; switched is set (old, new branch) when the branch changed
// within the same repository.
func (w *Window) refreshGit(now time.Time) (changed bool, switched [2]string) {
	old := w.git
	switch {
	case w.dir == "":
		w.git = gitInfo{}
	case w.dir != old.dir || now.Sub(old.found) >= gitRecheckEvery:
		w.git = findGit(w.dir, now)
	case old.headPath != "":
		// Same directory: has HEAD changed (a checkout)?
		fi, err := os.Stat(old.headPath)
		if err != nil {
			w.git = findGit(w.dir, now) // the repository went away?
		} else if !fi.ModTime().Equal(old.headMod) || fi.Size() != old.headSize {
			w.git.branch, w.git.headMod, w.git.headSize = readGitHead(old.headPath)
		}
	}
	if w.git.label() == old.label() {
		// (frameText changes only with the label)
		return false, switched
	}
	if old.repo != "" && w.git.top == old.top && w.git.branch != old.branch {
		switched = [2]string{old.branch, w.git.branch}
	}
	return true, switched
}

// findGit looks for the repository dir is in.
func findGit(dir string, now time.Time) gitInfo {
	g := gitInfo{dir: dir, found: now}
	for d := filepath.Clean(dir); ; {
		gitDir, ok := gitDirAt(d)
		if ok {
			g.top = d
			g.headPath = filepath.Join(gitDir, "HEAD")
			g.branch, g.headMod, g.headSize = readGitHead(g.headPath)
			if g.headMod.IsZero() { // no HEAD: not a repository after all
				return gitInfo{dir: dir, found: now}
			}
			g.repo = gitRepoName(gitCommonDir(gitDir), d)
			return g
		}
		parent := filepath.Dir(d)
		if parent == d {
			return g
		}
		d = parent
	}
}

// gitDirAt returns d's git directory: d/.git, or where a .git file points
// ("gitdir: ...", for worktrees and submodules).
func gitDirAt(d string) (string, bool) {
	p := filepath.Join(d, ".git")
	fi, err := os.Stat(p)
	switch {
	case err != nil:
		return "", false
	case fi.IsDir():
		return p, true
	case fi.Mode().IsRegular():
		data, err := readSmallFile(p, 4<<10)
		if err != nil {
			return "", false
		}
		target, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
		if !ok {
			return "", false
		}
		target = strings.TrimSpace(target)
		if !filepath.IsAbs(target) {
			target = filepath.Join(d, target)
		}
		return target, true
	}
	return "", false
}

// gitCommonDir is where a worktree's shared files (config) are: the
// "commondir" file names it; otherwise gitDir itself.
func gitCommonDir(gitDir string) string {
	data, err := readSmallFile(filepath.Join(gitDir, "commondir"), 4<<10)
	if err != nil {
		return gitDir
	}
	c := strings.TrimSpace(string(data))
	if !filepath.IsAbs(c) {
		c = filepath.Join(gitDir, c)
	}
	return c
}

// readGitHead reads HEAD: the branch it points to, or "detached <commit>".
func readGitHead(path string) (branch string, mod time.Time, size int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", time.Time{}, 0
	}
	data, err := readSmallFile(path, 4<<10)
	if err != nil {
		return "", time.Time{}, 0
	}
	head := strings.TrimSpace(string(data))
	if ref, ok := strings.CutPrefix(head, "ref:"); ok {
		ref = strings.TrimSpace(ref)
		if b, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			branch = b
		} else {
			branch = strings.TrimPrefix(ref, "refs/")
		}
	} else if len(head) >= 7 {
		branch = "detached " + head[:7]
	}
	return cleanGitText(branch), fi.ModTime(), fi.Size()
}

// gitRepoName is the repository's "owner/name": from the "origin" remote's
// URL (else the first remote's) in commonDir/config; else top's folder name
// (no owner).
func gitRepoName(commonDir, top string) string {
	if url := gitRemoteURL(filepath.Join(commonDir, "config")); url != "" {
		if n := repoFromURL(url); n != "" {
			return n
		}
	}
	return cleanGitText(filepath.Base(top))
}

// gitRemoteURL reads the origin remote's URL from a git config file (the
// first remote's if there is no origin).
func gitRemoteURL(configPath string) string {
	data, err := readSmallFile(configPath, 256<<10)
	if err != nil {
		return ""
	}
	var first, origin, section string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || line[0] == '#' || line[0] == ';':
		case strings.HasPrefix(line, "["):
			section = strings.ToLower(strings.Join(strings.Fields(strings.Trim(line, "[]")), " "))
		case strings.HasPrefix(section, `remote "`):
			key, value, ok := strings.Cut(line, "=")
			if !ok || strings.ToLower(strings.TrimSpace(key)) != "url" {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), `"`)
			if section == `remote "origin"` && origin == "" {
				origin = value
			}
			if first == "" {
				first = value
			}
		}
	}
	if origin != "" {
		return origin
	}
	return first
}

// repoFromURL is "owner/name" from a remote URL, or just "name" when the
// URL has no owner (a local path, a URL with only the name):
//
//	git@github.com:nontechno/goat.git         nontechno/goat
//	https://github.com/nontechno/goat         nontechno/goat
//	ssh://git@host:22/team/project.git        team/project
//	https://gitlab.com/group/sub/project.git  group/sub/project (subgroups)
//	https://dev.azure.com/org/proj/_git/repo  org/proj/repo
//	/srv/git/thing.git, file:///srv/thing     thing (local: no owner)
func repoFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	path, local := "", false
	switch {
	case strings.Contains(raw, "://"):
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		path, local = u.Path, u.Scheme == "file"
	case isSCPLike(raw): // [user@]host:owner/name
		_, path, _ = strings.Cut(raw, ":")
	default: // a path on this machine (or a share)
		path, local = raw, true
	}
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' })
	parts = slices.DeleteFunc(parts, func(p string) bool { return p == "_git" }) // Azure DevOps
	if len(parts) == 0 {
		return ""
	}
	name := cleanGitText(parts[len(parts)-1])
	if local || len(parts) == 1 || name == "" {
		return name
	}
	owner := cleanGitText(strings.Join(parts[:len(parts)-1], "/"))
	if owner == "" {
		return name
	}
	return owner + "/" + name
}

// isSCPLike reports whether s is git's scp-like syntax, host:path: a colon
// before any slash, and not a Windows drive (C:\...).
func isSCPLike(s string) bool {
	colon := strings.IndexByte(s, ':')
	if colon <= 1 { // none, or a drive letter
		return false
	}
	slash := strings.IndexAny(s, "/\\")
	return slash < 0 || colon < slash
}

// cleanGitText keeps a name or branch safe to show: printable characters
// only, trimmed, at most 32 characters.
func cleanGitText(s string) string {
	return cleanHostName(s)
}

// gitFrameText is w's git label for its frame ("" when not shown).
func (m *WM) gitFrameText(w *Window) string {
	if !m.cfg.ShowGit || w.procName == "ssh" {
		return ""
	}
	return w.git.frameText(m.plainIdent)
}

// evGit lists branch switches in the events window.
const evGit eventKind = "git"

// refreshPlace updates w's directory (for status_show_dir, the active
// window's; for show_git, every window's) and, with show_git, its git
// repository and branch; a branch switch is listed in the events window.
// Reports whether anything shown changed.
func (m *WM) refreshPlace(w *Window, now time.Time) bool {
	if !m.cfg.ShowGit && !(m.cfg.StatusShowDir && w == m.focused) {
		return false
	}
	changed := w.refreshDir()
	if m.cfg.ShowGit {
		gitChanged, sw := w.refreshGit(now)
		if sw != [2]string{} {
			m.logEvent(w, evGit, w.git.repo+": "+sw[0]+" "+changeSymbol+" "+sw[1])
		}
		changed = changed || gitChanged
	}
	return changed
}
