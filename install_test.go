package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// installServer serves every install file as "<dir>/<name> v<version>".
func installServer(t *testing.T, version *atomic.Int32, missing string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if missing != "" && strings.HasSuffix(r.URL.Path, "/"+missing) {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(strings.TrimPrefix(r.URL.Path, "/") + " v" + string(rune('0'+version.Load())) + "\n"))
	}))
	t.Cleanup(srv.Close)
	old := installBase
	installBase = srv.URL + "/"
	t.Cleanup(func() { installBase = old })
}

func TestInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	var ver atomic.Int32
	ver.Store(1)
	installServer(t, &ver, "")
	dir := filepath.Join(home, ".config", "goat")

	var out bytes.Buffer
	if err := install(&out); err != nil {
		t.Fatal(err)
	}
	for _, f := range installFiles {
		b, err := os.ReadFile(filepath.Join(dir, f.name))
		if err != nil || string(b) != f.dir+"/"+f.name+" v1\n" {
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
	if b, _ := os.ReadFile(cfg); string(b) != "configs/config.toml v2\n" {
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
	installServer(t, &ver, "local.toml") // the last file is missing
	err := install(&bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(home, ".config", "goat"))
	if len(entries) != 0 {
		t.Fatalf("files written despite the failure: %v", entries)
	}
}

func TestInstallUsesXDGConfigHome(t *testing.T) {
	x := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", x)
	if d, _ := configDir(); d != filepath.Join(x, "goat") {
		t.Fatalf("%q", d)
	}
}
