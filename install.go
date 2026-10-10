package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// "goat -install": fetch goat's color and config files into the config
// directory, then exit.
//
// Every file is downloaded before anything is written, so a failed download
// leaves the directory as it was. A file already there with the same
// contents is left alone; one with different contents (e.g. edited by hand)
// is kept as <name>.<time>.bak before the new one replaces it.

// installBase is where the files are fetched from; $GOAT_INSTALL_URL
// overrides it.
var installBase = "https://goat.nontechno.com/"

// installFiles are the files -install fetches: a directory on the server
// and the file name, which is also the name in the config directory.
var installFiles = []struct{ dir, name string }{
	{"colors", "dark-background.color"},
	{"colors", "dark-foreground.color"},
	{"colors", "light-background.color"},
	{"colors", "light-foreground.color"},
	{"configs", "config.toml"},
	{"configs", "local.toml"},
}

const (
	installMaxSize = 4 << 20 // per file; these are small text files
	installTimeout = 30 * time.Second
)

// configDir is the directory goat reads its config from:
// $XDG_CONFIG_HOME/goat, else ~/.config/goat.
func configDir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "goat"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("can't find the home directory: %w", err)
	}
	return filepath.Join(h, ".config", "goat"), nil
}

// install runs "goat -install", reporting progress to out.
func install(out io.Writer) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	fmt.Fprintf(out, "installing into %s\n", dir)

	base := installBase
	if v := os.Getenv("GOAT_INSTALL_URL"); v != "" { // a mirror, or a test server
		base = strings.TrimSuffix(v, "/") + "/"
	}

	// Download everything first.
	client := &http.Client{Timeout: installTimeout}
	got := make([][]byte, len(installFiles))
	for i, f := range installFiles {
		url := base + f.dir + "/" + f.name
		data, err := download(client, url)
		if err != nil {
			return fmt.Errorf("%w (nothing was changed)", err)
		}
		got[i] = data
		fmt.Fprintf(out, "  downloaded %-24s %6d bytes\n", f.name, len(data))
	}

	// Then put each file in place.
	stamp := time.Now().Format("20060102-150405")
	for i, f := range installFiles {
		dst := filepath.Join(dir, f.name)
		old, err := os.ReadFile(dst)
		switch {
		case err == nil && bytes.Equal(old, got[i]):
			fmt.Fprintf(out, "  %-35s unchanged\n", f.name)
			continue
		case err == nil:
			bak := dst + "." + stamp + ".bak"
			if err := os.Rename(dst, bak); err != nil {
				return fmt.Errorf("keeping a backup of %s: %w", dst, err)
			}
			fmt.Fprintf(out, "  %-35s replaced (yours kept as %s)\n", f.name, filepath.Base(bak))
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(out, "  %-35s installed\n", f.name)
		default:
			return fmt.Errorf("reading %s: %w", dst, err)
		}
		if err := writeFileAtomic(dst, got[i], 0o644); err != nil {
			return err
		}
	}
	logger.Info("install", "dir", dir, "from", base)
	fmt.Fprintln(out, "done")
	return nil
}

// download fetches url (HTTP 200 only, at most installMaxSize bytes).
func download(client *http.Client, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "goat/"+version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, installMaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	if len(data) > installMaxSize {
		return nil, fmt.Errorf("downloading %s: larger than %d bytes", url, installMaxSize)
	}
	return data, nil
}

// writeFileAtomic writes data to path through a temporary file and a
// rename, so the file is never seen half written.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(name, perm)
	}
	if werr == nil {
		werr = os.Rename(name, path)
	}
	if werr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("writing %s: %w", path, werr)
	}
	return nil
}
