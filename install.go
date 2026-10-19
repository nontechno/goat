package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// "goat -install": fetch goat's config files and the palettes
// (terminal-color-combinations.json) into the config directory, then exit.
//
// Every file is downloaded before anything is written, so a failed download
// leaves the directory as it was. A file already there with the same
// contents is left alone; one with different contents (e.g. edited by hand)
// is kept as <name>.<time>.bak before the new one replaces it.

// installSources are where the files are fetched from, in order: each file
// comes from the first that delivers it (a download that fails, or isn't a
// valid file of its kind, is tried from the next). $GOAT_INSTALL_URL
// replaces them with one source of your own.
var installSources = []string{
	"https://goat.nontechno.com/",
	"https://raw.githubusercontent.com/nontechno/goat/refs/heads/resources/", // backup
}

// installFiles are the files -install fetches: a directory on the server
// and the file name, which is also the name in the config directory. The
// palettes come from their own sources (combosSources) instead.
var installFiles = []struct {
	dir, name string
	palettes  bool // from combosSources
}{
	{dir: "configs", name: "config.toml"},
	{dir: "configs", name: "local.toml"},
	{dir: "colors", name: combosFile, palettes: true}, // palettes (combos.go)
}

// combosSources are where the palettes come from:
// https://github.com/nontechno/color. $GOAT_INSTALL_URL replaces them too
// (<url>/colors/terminal-color-combinations.json).
var combosSources = []string{
	"https://raw.githubusercontent.com/nontechno/color/main/" + combosFile,
	"https://color.nontechno.com/" + combosFile, // backup
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

	sources := installSources
	if v := os.Getenv("GOAT_INSTALL_URL"); v != "" { // a mirror, or a test server
		sources = []string{strings.TrimSuffix(v, "/") + "/"}
	}

	// Download everything first.
	client := &http.Client{Timeout: installTimeout}
	got := make([][]byte, len(installFiles))
	from := make([]string, len(installFiles))
	for i, f := range installFiles {
		var errs []string
		var urls []string
		if f.palettes {
			urls = combosSources
		}
		if len(urls) == 0 || os.Getenv("GOAT_INSTALL_URL") != "" {
			urls = nil
			for _, base := range sources {
				urls = append(urls, base+f.dir+"/"+f.name)
			}
		}
		for si, url := range urls {
			base := url
			data, err := download(client, url)
			if err == nil {
				err = checkInstallFile(f.name, data)
			}
			if err == nil {
				got[i], from[i] = data, base
				break
			}
			errs = append(errs, url+": "+err.Error())
			logger.Warn("install: download failed", "url", url, "err", err)
			if si+1 < len(urls) {
				fmt.Fprintf(out, "  %-24s %s: %v - trying %s\n", f.name, hostOf(base), err, hostOf(urls[si+1]))
			}
		}
		if got[i] == nil {
			return fmt.Errorf("%s: %s (nothing was changed)", f.name, strings.Join(errs, "; "))
		}
		fmt.Fprintf(out, "  downloaded %-24s %6d bytes  (%s)\n", f.name, len(got[i]), hostOf(from[i]))
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
	logger.Info("install", "dir", dir, "from", strings.Join(slices.Compact(slices.Clone(from)), " "))
	fmt.Fprintln(out, "done")
	return nil
}

// checkInstallFile makes sure a downloaded file is what its name says (an
// error page served as "200 OK" isn't): the palettes must be palettes, a
// .toml file valid TOML.
func checkInstallFile(name string, data []byte) error {
	switch {
	case name == combosFile:
		if _, _, err := parseCombos(data); err != nil {
			return err
		}
	case strings.HasSuffix(name, ".toml"):
		var v map[string]any
		if _, err := toml.Decode(string(data), &v); err != nil {
			return fmt.Errorf("not valid TOML: %w", err)
		}
	}
	return nil
}

// hostOf is the host part of a URL, for messages.
func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return u
}

// download fetches u (HTTP 200 only, at most installMaxSize bytes). Its
// errors don't repeat the URL: the caller adds it.
func download(client *http.Client, u string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "goat/"+version)
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // without the "Get <url>:" around it
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, installMaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > installMaxSize {
		return nil, fmt.Errorf("larger than %d bytes", installMaxSize)
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
