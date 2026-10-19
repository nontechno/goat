package main

import (
	"runtime/debug"
	"testing"
)

func TestVersionFromBuild(t *testing.T) {
	bi := func(v string, settings ...string) *debug.BuildInfo {
		b := &debug.BuildInfo{Main: debug.Module{Version: v}}
		for i := 0; i+1 < len(settings); i += 2 {
			b.Settings = append(b.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
		}
		return b
	}
	for _, tc := range []struct {
		name string
		b    *debug.BuildInfo
		ok   bool
		want string
	}{
		{"none", nil, false, "dev"},
		{"go install @v1.2.0", bi("v1.2.0"), true, "1.2.0"},
		{"pseudo-version", bi("v0.0.0-20261019120000-3f2a9c1d2e3f", "vcs.revision", "3f2a9c1d2e3f"), true, "dev-3f2a9c1"},
		{"local build", bi("(devel)", "vcs.revision", "3f2a9c1d2e3f4a", "vcs.modified", "true"), true, "dev-3f2a9c1+"},
		{"no vcs", bi("(devel)"), true, "dev"},
	} {
		if got := versionFromBuild(tc.b, tc.ok); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
