package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// installContent is what a test server serves for file f: a valid file of
// its kind, marked with the server's name and a version.
func installContent(path, server string, version int32) string {
	mark := fmt.Sprintf("%s %s v%d", server, path, version)
	if strings.HasSuffix(path, combosFile) {
		return fmt.Sprintf(`[{"id":1,"Theme":"dark","Background.rgb":"#000000","Background.ansi":16,`+
			`"Text.rgb":"#ffffff","Text.ansi":231,"Pedantic":%q}]`+"\n", mark)
	}
	return "# " + mark + "\n"
}

func wantContent(f struct {
	dir, name string
	palettes  bool
}, server string, version int32) string {
	return installContent(f.dir+"/"+f.name, server, version)
}

// installServer serves every install file (see installContent). Files
// named in broken get a 404 ("missing") or an HTML error page with 200
// ("garbage").
func installServer(t *testing.T, name string, version *atomic.Int32, broken map[string]string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		switch broken[filepath.Base(path)] {
		case "missing":
			http.NotFound(w, r)
			return
		case "garbage":
			_, _ = w.Write([]byte("<html><body>Oops</body></html>\n"))
			return
		}
		_, _ = w.Write([]byte(installContent(path, name, version.Load())))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/"
}

// useSources points -install at the given servers for the test.
func useSources(t *testing.T, sources ...string) {
	old, oldCombos := installSources, combosSources
	installSources = sources
	combosSources = nil // the palettes from the test servers too
	for _, s := range sources {
		combosSources = append(combosSources, s+"colors/"+combosFile)
	}
	t.Cleanup(func() { installSources, combosSources = old, oldCombos })
	t.Setenv("GOAT_INSTALL_URL", "")
}

func TestInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	var ver atomic.Int32
	ver.Store(1)
	useSources(t, installServer(t, "main", &ver, nil))
	dir := filepath.Join(home, ".config", "goat")

	var out bytes.Buffer
	if err := install(&out); err != nil {
		t.Fatal(err)
	}
	for _, f := range installFiles {
		b, err := os.ReadFile(filepath.Join(dir, f.name))
		if err != nil || string(b) != wantContent(f, "main", 1) {
			t.Fatalf("%s: %q %v", f.name, b, err)
		}
	}
	if !strings.Contains(out.String(), "installed") || !strings.Contains(out.String(), "done") {
		t.Fatalf("output:\n%s", out.String())
	}

	// Again, same files: nothing changes, no backups.
	out.Reset()
	if err := install(&out); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "unchanged") != len(installFiles) {
		t.Fatalf("output:\n%s", out.String())
	}

	// A file edited by hand is kept as a backup when a new version comes.
	cfg := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(cfg, []byte("my settings\n"), 0o644)
	ver.Store(2)
	out.Reset()
	if err := install(&out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "# main configs/config.toml v2\n" {
		t.Fatalf("config.toml = %q", b)
	}
	baks, _ := filepath.Glob(cfg + ".*.bak")
	if len(baks) != 1 {
		t.Fatalf("backups: %v", baks)
	}
	if b, _ := os.ReadFile(baks[0]); string(b) != "my settings\n" {
		t.Fatalf("backup = %q", b)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestInstallFailureChangesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	var ver atomic.Int32
	ver.Store(1)
	// The last file is missing from both sources.
	gone := map[string]string{"local.toml": "missing"}
	useSources(t, installServer(t, "main", &ver, gone), installServer(t, "backup", &ver, gone))
	err := install(&bytes.Buffer{})
	if err == nil || strings.Count(err.Error(), "404 Not Found") != 2 /* (not "404": the port may hold one) */ || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(home, ".config", "goat"))
	if len(entries) != 0 {
		t.Fatalf("files written despite the failure: %v", entries)
	}
}

// A file the main source can't deliver (missing, an error page, or the
// whole site down) comes from the backup; the rest from the main one.
func TestInstallFallsBackToBackup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	var ver atomic.Int32
	ver.Store(1)
	main := installServer(t, "main", &ver, map[string]string{
		"config.toml": "missing", combosFile: "garbage", "local.toml": "garbage",
	})
	backup := installServer(t, "backup", &ver, nil)
	useSources(t, main, backup)
	var out bytes.Buffer
	if err := install(&out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	dir := filepath.Join(home, ".config", "goat")
	for _, f := range installFiles {
		want := "main"
		if f.name == "config.toml" || f.name == combosFile || f.name == "local.toml" {
			want = "backup"
		}
		if b, _ := os.ReadFile(filepath.Join(dir, f.name)); string(b) != wantContent(f, want, 1) {
			t.Errorf("%s = %q, want the %s copy", f.name, b, want)
		}
	}
	if strings.Count(out.String(), "trying 127.0.0.1") != 3 {
		t.Fatalf("output:\n%s", out.String())
	}

	// Main site down altogether: everything from the backup.
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	useSources(t, dead.URL+"/", backup)
	ver.Store(2)
	out.Reset()
	if err := install(&out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	for _, f := range installFiles {
		if b, _ := os.ReadFile(filepath.Join(dir, f.name)); string(b) != wantContent(f, "backup", 2) {
			t.Errorf("site down: %s = %q", f.name, b)
		}
	}
}

// The real sources: the main site, then the GitHub backup.
func TestInstallSources(t *testing.T) {
	if len(installSources) != 2 || installSources[0] != "https://goat.nontechno.com/" ||
		installSources[1] != "https://raw.githubusercontent.com/nontechno/goat/refs/heads/resources/" {
		t.Fatalf("sources: %v", installSources)
	}
}

func TestInstallUsesXDGConfigHome(t *testing.T) {
	x := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	if d, _ := configDir(); d != filepath.Join(x, "goat") {
		t.Fatalf("%q", d)
	}
}

// The palettes' real sources: color.nontechno.com, then GitHub.
func TestInstallCombosSources(t *testing.T) {
	want := []string{
		"https://color.nontechno.com/terminal-color-combinations.json",
		"https://raw.githubusercontent.com/nontechno/color/refs/heads/main/terminal-color-combinations.json",
	}
	if len(combosSources) != 2 || combosSources[0] != want[0] || combosSources[1] != want[1] {
		t.Fatalf("sources: %v", combosSources)
	}
}

// Writing a config file through a link replaces the file it points to,
// not the link, and keeps the file's permissions.
func TestWriteFileAtomicLink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "system.toml")
	_ = os.MkdirAll(filepath.Dir(real), 0o755)
	if err := os.WriteFile(real, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "system.toml")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	if err := writeFileAtomic(link, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was replaced")
	}
	if b, _ := os.ReadFile(real); string(b) != "new\n" {
		t.Fatalf("target: %q", b)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o600 {
		t.Errorf("permissions %v", fi.Mode().Perm())
	}
}
