//go:build darwin

package paths

import (
	"path/filepath"
	"testing"
)

func TestFromEnvDarwin(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	defaults := Roots{
		Claude:       filepath.Join(home, ".claude"),
		Codex:        filepath.Join(home, ".codex"),
		OpenCodeData: filepath.Join(home, ".local", "share", "opencode"),
		Cache:        filepath.Join(home, "Library", "Caches", "agent-sessions"),
		Config:       filepath.Join(home, "Library", "Application Support", "agent-sessions"),
	}

	tests := []struct {
		name string
		env  map[string]string
		want Roots
	}{
		{name: "home fallback", want: defaults},
		{name: "Claude override", env: map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/custom_claude"}, want: Roots{
			Claude: "/tmp/custom_claude", Codex: defaults.Codex, OpenCodeData: defaults.OpenCodeData, Cache: defaults.Cache, Config: defaults.Config,
		}},
		{name: "Codex override", env: map[string]string{"CODEX_HOME": "/opt/codex"}, want: Roots{
			Claude: defaults.Claude, Codex: "/opt/codex", OpenCodeData: defaults.OpenCodeData, Cache: defaults.Cache, Config: defaults.Config,
		}},
		{name: "data override", env: map[string]string{"XDG_DATA_HOME": "/opt/data"}, want: Roots{
			Claude: defaults.Claude, Codex: defaults.Codex, OpenCodeData: "/opt/data/opencode", Cache: defaults.Cache, Config: defaults.Config,
		}},
		{name: "relative values ignored", env: map[string]string{
			"CLAUDE_CONFIG_DIR": "relative/claude", "CODEX_HOME": "./codex", "XDG_DATA_HOME": "data",
		}, want: defaults},
		{name: "empty values ignored", env: map[string]string{
			"CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "", "XDG_DATA_HOME": "",
		}, want: defaults},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FromEnv(func(key string) string { return tt.env[key] }, home)
			if got != tt.want {
				t.Errorf("FromEnv() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
