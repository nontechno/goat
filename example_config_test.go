package main

import (
	"os"
	"testing"
)

// The shipped example must parse cleanly and match the built-in defaults.
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
		c.ScrollbackLines != d.ScrollbackLines || c.MacOptionKeys != d.MacOptionKeys || c.StatusShowDir != d.StatusShowDir || c.ShowHost != d.ShowHost || c.LogFile != d.LogFile || c.LogLevel != d.LogLevel || c.TitleSource != d.TitleSource ||
		c.ClipboardOSC52 != d.ClipboardOSC52 || c.CopyCommand != d.CopyCommand ||
		len(c.Theme.WindowColors) != len(d.Theme.WindowColors) ||
		c.Theme.NewWindowNextColors != d.Theme.NewWindowNextColors {
		t.Fatalf("example differs from defaults:\n%+v\n%+v", c, d)
	}
	for i, bg := range c.Theme.WindowColors {
		if bg != d.Theme.WindowColors[i] {
			t.Fatalf("window_colors[%d]: %v vs %v", i, bg, d.Theme.WindowColors[i])
		}
	}
}
