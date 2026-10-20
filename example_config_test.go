package main

import (
	"os"
	"testing"
)

// The shipped example must parse cleanly and match the built-in defaults
// (it documents them: your own values go in your config.toml).
func TestExampleConfig(t *testing.T) {
	data, err := os.ReadFile("config.example.toml")
	if err != nil {
		t.Fatal(err)
	}
	c, warns := parseConfig(string(data))
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	d := DefaultConfig()
	if c.Keys != d.Keys || c.Layout != d.Layout || c.AltTimeoutMs != d.AltTimeoutMs ||
		c.ScrollbackLines != d.ScrollbackLines || c.Frame != d.Frame || c.WrapLines != d.WrapLines ||
		c.NowrapWidth != d.NowrapWidth || c.ShowGit != d.ShowGit || c.TrueColor != d.TrueColor || c.PaletteColors != d.PaletteColors ||
		c.KeyboardEnhancements != d.KeyboardEnhancements || c.MacOptionArrows != d.MacOptionArrows ||
		c.RepaintSeconds != d.RepaintSeconds || c.DisableMouse != d.DisableMouse || c.MacOptionKeys != d.MacOptionKeys || c.StatusShowDir != d.StatusShowDir || c.ShowHost != d.ShowHost || c.ShowUser != d.ShowUser || c.LogFile != d.LogFile || c.LogLevel != d.LogLevel || c.TitleSource != d.TitleSource ||
		c.ClipboardOSC52 != d.ClipboardOSC52 || c.CopyCommand != d.CopyCommand ||
		c.Theme.NewWindowNextColors != d.Theme.NewWindowNextColors || c.Theme.MinTextContrast != d.Theme.MinTextContrast {
		t.Fatalf("example differs from defaults:\n%+v\n%+v", c, d)
	}
}
