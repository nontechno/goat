package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Aliases: shorter or friendlier names shown instead of the real ones, from
// ~/.config/goat/alias.toml:
//
//	[host]                        # host names: goat's, and where ssh logs in
//	"build-server-07.corp.example.com" = "build"
//	"*.corp.example.com" = "corp"
//
//	[directory]                   # the start of a directory path
//	"/home/opc" = "🏠"            # /home/opc/src/goat shows as 🏠/src/goat
//	"~/src/github.com" = "gh"
//
//	[repo]                        # git repositories: owner/name, or the name
//	"nontechno/goat" = "goat"
//
//	[branch]                      # branches: repo/branch, or the branch
//	"*/main" = "🏡"               # main, in any repository
//	"nontechno/goat/feature/x" = "monday"
//
// Keys are the real names; values are what is shown. In a key, * stands for
// any characters ("/" included) and ? for one; a key without them must match
// exactly, and wins over patterns, which are tried in the file's order. Host
// names match whatever their case. A directory key matches the directory and
// everything under it (the longest such key wins), and only its part of the
// path is replaced; it may start with ~ (your home folder). Repository and
// branch keys use the real names (owner/name as the remote has it), not
// other aliases.
//
// Aliases are only shown: frames, the status bar, the events window. Real
// names are still what goat works with (new windows start in the real
// directory, and so on). The file is re-read when it changes, every few
// seconds; a mistake in it is listed in the events window and the file's
// aliases are not used until it is fixed.

// aliasKinds are alias.toml's sections.
var aliasKinds = []string{"host", "directory", "repo", "branch"}

// aliasRule is one line of a section.
type aliasRule struct {
	from, to string
	glob     bool // from has * or ?
}

// aliases are alias.toml's rules, per section.
type aliases struct {
	rules map[string][]aliasRule // section → rules in file order
	home  string                 // for ~ in directory keys
}

// aliasPath is alias.toml in goat's config directory ("" if there is none).
func aliasPath() string {
	dir, err := configDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "alias.toml")
}

// parseAliases reads alias.toml's text. Unknown sections and values that
// aren't strings are mistakes; so is anything that isn't valid TOML.
func parseAliases(text, home string) (aliases, error) {
	a := aliases{rules: map[string][]aliasRule{}, home: home}
	var raw map[string]map[string]string
	md, err := toml.Decode(text, &raw)
	if err != nil {
		return a, err
	}
	for kind := range raw {
		if !contains(aliasKinds, kind) {
			return a, fmt.Errorf("unknown section [%s] (want [%s])", kind, strings.Join(aliasKinds, "], ["))
		}
	}
	// md.Keys() is in the file's order: rules are tried in that order.
	for _, k := range md.Keys() {
		if len(k) != 2 {
			continue
		}
		kind, from := k[0], k[1]
		to := cleanAlias(raw[kind][from])
		if from == "" {
			continue
		}
		a.rules[kind] = append(a.rules[kind], aliasRule{from: from, to: to,
			glob: strings.ContainsAny(from, "*?")})
	}
	return a, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// cleanAlias keeps an alias safe to show: printable characters only,
// trimmed, at most 32 characters (like a host name).
func cleanAlias(s string) string { return cleanHostName(s) }

// lookup is the alias of name in a section: an exact key first, then the
// first pattern that matches. ok is false when nothing matches.
func (a aliases) lookup(kind, name string, fold bool) (string, bool) {
	eq := func(x, y string) bool {
		if fold {
			return strings.EqualFold(x, y)
		}
		return x == y
	}
	for _, r := range a.rules[kind] {
		if !r.glob && eq(r.from, name) {
			return r.to, true
		}
	}
	for _, r := range a.rules[kind] {
		if r.glob && globMatch(r.from, name, fold) {
			return r.to, true
		}
	}
	return "", false
}

// host is what to show for a host name.
func (a aliases) host(name string) string {
	if to, ok := a.lookup("host", name, true); ok && to != "" && name != "" {
		return to
	}
	return name
}

// repo is what to show for a repository ("owner/name", or "name"): its
// alias by the full name, else by the name alone.
func (a aliases) repo(repo string) string {
	if repo == "" {
		return ""
	}
	if to, ok := a.lookup("repo", repo, false); ok && to != "" {
		return to
	}
	if i := strings.LastIndexByte(repo, '/'); i >= 0 {
		if to, ok := a.lookup("repo", repo[i+1:], false); ok && to != "" {
			return to
		}
	}
	return repo
}

// branch is what to show for a branch of a repository: its alias by
// "owner/name/branch", "name/branch" or the branch alone, in that order.
func (a aliases) branch(repo, branch string) string {
	if branch == "" {
		return ""
	}
	tries := []string{repo + "/" + branch}
	if i := strings.LastIndexByte(repo, '/'); i >= 0 {
		tries = append(tries, repo[i+1:]+"/"+branch)
	}
	tries = append(tries, branch)
	for _, t := range tries {
		if to, ok := a.lookup("branch", t, false); ok && to != "" {
			return to
		}
	}
	return branch
}

// dir is what to show for a directory: the longest [directory] key that is
// the directory or a folder above it is replaced by its alias; a pattern
// key replaces the whole path it matches. "" if no key matches.
func (a aliases) dir(path string) (string, bool) {
	best, bestTo := "", ""
	for _, r := range a.rules["directory"] {
		if r.glob || r.to == "" {
			continue
		}
		from := filepath.Clean(aliasHome(r.from, a.home))
		if (path == from || strings.HasPrefix(path, strings.TrimSuffix(from, "/")+"/")) && len(from) > len(best) {
			best, bestTo = from, r.to
		}
	}
	if best != "" {
		return bestTo + path[len(best):], true
	}
	for _, r := range a.rules["directory"] {
		if r.glob && r.to != "" && globMatch(aliasHome(r.from, a.home), path, false) {
			return r.to, true
		}
	}
	return "", false
}

// aliasHome turns a leading ~ into the home folder.
func aliasHome(p, home string) string {
	if home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return p
}

// globMatch reports whether s matches pattern: * is any characters ("/"
// included), ? is one character, everything else stands for itself.
func globMatch(pattern, s string, fold bool) bool {
	if fold {
		pattern, s = strings.ToLower(pattern), strings.ToLower(s)
	}
	p, t := []rune(pattern), []rune(s)
	// Iterative matching with backtracking to the last *.
	pi, ti, star, mark := 0, 0, -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ti = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// ---- goat's side --------------------------------------------------------------

// aliasRecheckEvery is how often alias.toml is looked at for changes.
const aliasRecheckEvery = 3 * time.Second

// aliasState is alias.toml as goat last read it.
type aliasState struct {
	path    string
	a       aliases
	mod     time.Time // the file's modification time when read
	size    int64
	checked time.Time
	err     string // the file's mistake, if any (listed once per change)
}

// loadAliases reads alias.toml (now, and again whenever it changes).
// Reports whether what is shown may have changed.
func (m *WM) loadAliases(now time.Time) bool {
	st := &m.alias
	if st.path == "" {
		return false
	}
	st.checked = now
	fi, err := os.Stat(st.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		changed := len(st.a.rules) > 0 || !st.mod.IsZero()
		*st = aliasState{path: st.path, checked: now, a: aliases{home: m.home}}
		return changed
	case err != nil:
		return false
	case fi.ModTime().Equal(st.mod) && fi.Size() == st.size:
		return false // unchanged
	}
	st.mod, st.size = fi.ModTime(), fi.Size()
	data, err := readSmallFile(st.path, 256<<10)
	if err == nil {
		var a aliases
		if a, err = parseAliases(string(data), m.home); err == nil {
			st.a, st.err = a, ""
			n := 0
			for _, rs := range a.rules {
				n += len(rs)
			}
			logger.Info("aliases loaded", "file", st.path, "aliases", n)
			return true
		}
	}
	msg := fmt.Sprintf("%s: %v (aliases not used until it's fixed)", homeShort(st.path, m.home), err)
	if msg != st.err {
		st.err = msg
		logger.Warn("aliases", "err", err, "file", st.path)
		m.logEvent(nil, evConfig, msg)
	}
	changed := len(st.a.rules) > 0
	st.a = aliases{home: m.home}
	return changed
}

// refreshAliases re-reads alias.toml if it changed (every
// aliasRecheckEvery); reports whether what is shown may have changed.
func (m *WM) refreshAliases(now time.Time) bool {
	if m.alias.path == "" || now.Sub(m.alias.checked) < aliasRecheckEvery {
		return false
	}
	return m.loadAliases(now)
}

// showHost is the host name to show (its alias, if it has one).
func (m *WM) showHost(name string) string { return m.alias.a.host(name) }

// showDir is a directory as the status bar shows it: aliased if a
// [directory] key covers it, else with ~ for the home folder.
func (m *WM) showDir(dir string) string {
	if d, ok := m.alias.a.dir(dir); ok {
		return d
	}
	return homeShort(dir, m.home)
}
