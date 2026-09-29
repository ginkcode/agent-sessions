//go:build linux

package paths

import (
	"path/filepath"
	"testing"
)

func TestFromEnvLinux(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	defaults := Roots{
		Claude:       filepath.Join(home, ".claude"),
		Codex:        filepath.Join(home, ".codex"),
		OpenCodeData: filepath.Join(home, ".local", "share", "opencode"),
		Cache:        filepath.Join(home, ".cache", "agent-sessions"),
		Config:       filepath.Join(home, ".config", "agent-sessions"),
		Data:         filepath.Join(home, ".local", "share", "agent-sessions"),
	}

	tests := []struct {
		name string
		env  map[string]string
		want Roots
	}{
		{name: "home fallback", want: defaults},
		{name: "Claude override", env: map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/claude/../custom"}, want: Roots{
			Claude: "/tmp/custom", Codex: defaults.Codex, OpenCodeData: defaults.OpenCodeData, Cache: defaults.Cache, Config: defaults.Config, Data: defaults.Data,
		}},
		{name: "Codex override", env: map[string]string{"CODEX_HOME": "/opt/codex"}, want: Roots{
			Claude: defaults.Claude, Codex: "/opt/codex", OpenCodeData: defaults.OpenCodeData, Cache: defaults.Cache, Config: defaults.Config, Data: defaults.Data,
		}},
		{name: "data override", env: map[string]string{"XDG_DATA_HOME": "/opt/data"}, want: Roots{
			Claude: defaults.Claude, Codex: defaults.Codex, OpenCodeData: "/opt/data/opencode", Cache: defaults.Cache, Config: defaults.Config, Data: "/opt/data/agent-sessions",
		}},
		{name: "cache override", env: map[string]string{"XDG_CACHE_HOME": "/opt/cache"}, want: Roots{
			Claude: defaults.Claude, Codex: defaults.Codex, OpenCodeData: defaults.OpenCodeData, Cache: "/opt/cache/agent-sessions", Config: defaults.Config, Data: defaults.Data,
		}},
		{name: "config override", env: map[string]string{"XDG_CONFIG_HOME": "/opt/config"}, want: Roots{
			Claude: defaults.Claude, Codex: defaults.Codex, OpenCodeData: defaults.OpenCodeData, Cache: defaults.Cache, Config: "/opt/config/agent-sessions", Data: defaults.Data,
		}},
		{name: "relative values ignored", env: map[string]string{
			"CLAUDE_CONFIG_DIR": "relative/claude", "CODEX_HOME": "./codex", "XDG_DATA_HOME": "data", "XDG_CACHE_HOME": "../cache", "XDG_CONFIG_HOME": "config",
		}, want: defaults},
		{name: "empty values ignored", env: map[string]string{
			"CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "", "XDG_DATA_HOME": "", "XDG_CACHE_HOME": "", "XDG_CONFIG_HOME": "",
		}, want: defaults},
		{name: "all overrides", env: map[string]string{
			"CLAUDE_CONFIG_DIR": "/a", "CODEX_HOME": "/b", "XDG_DATA_HOME": "/c", "XDG_CACHE_HOME": "/d", "XDG_CONFIG_HOME": "/e",
		}, want: Roots{Claude: "/a", Codex: "/b", OpenCodeData: "/c/opencode", Cache: "/d/agent-sessions", Config: "/e/agent-sessions", Data: "/c/agent-sessions"}},
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

func TestDefaultLinux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(key, "")
	}
	got, err := Default()
	if err != nil {
		t.Fatalf("Default(): %v", err)
	}
	want := FromEnv(func(string) string { return "" }, home)
	if got != want {
		t.Errorf("Default() = %+v, want %+v", got, want)
	}
}
