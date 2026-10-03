//go:build windows

package paths

import (
	"path/filepath"
	"testing"
)

func TestFromEnvWindows(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	defaults := Roots{
		Claude:       filepath.Join(home, ".claude"),
		Codex:        filepath.Join(home, ".codex"),
		OpenCodeData: filepath.Join(home, ".local", "share", "opencode"),
		Cache:        filepath.Join(home, "AppData", "Local", "agent-sessions", "cache"),
		Config:       filepath.Join(home, "AppData", "Roaming", "agent-sessions"),
		Data:         filepath.Join(home, "AppData", "Local", "agent-sessions"),
	}

	tests := []struct {
		name string
		env  map[string]string
		want Roots
	}{
		{name: "home fallback", want: defaults},
		{name: "Claude override", env: map[string]string{"CLAUDE_CONFIG_DIR": `C:\tmp\claude\..\custom`}, want: Roots{
			Claude: `C:\tmp\custom`, Codex: defaults.Codex, OpenCodeData: defaults.OpenCodeData, Cache: defaults.Cache, Config: defaults.Config, Data: defaults.Data,
		}},
		{name: "Codex override", env: map[string]string{"CODEX_HOME": `D:\codex`}, want: Roots{
			Claude: defaults.Claude, Codex: `D:\codex`, OpenCodeData: defaults.OpenCodeData, Cache: defaults.Cache, Config: defaults.Config, Data: defaults.Data,
		}},
		{name: "XDG data override", env: map[string]string{"XDG_DATA_HOME": `C:\data`}, want: Roots{
			Claude: defaults.Claude, Codex: defaults.Codex, OpenCodeData: `C:\data\opencode`, Cache: defaults.Cache, Config: defaults.Config, Data: defaults.Data,
		}},
		{name: "profile dirs", env: map[string]string{
			"LOCALAPPDATA": `C:\Users\dev\AppData\Local`, "APPDATA": `C:\Users\dev\AppData\Roaming`,
		}, want: Roots{
			Claude: defaults.Claude, Codex: defaults.Codex, OpenCodeData: defaults.OpenCodeData,
			Cache:  `C:\Users\dev\AppData\Local\agent-sessions\cache`,
			Config: `C:\Users\dev\AppData\Roaming\agent-sessions`,
			Data:   `C:\Users\dev\AppData\Local\agent-sessions`,
		}},
		{name: "forward slashes cleaned", env: map[string]string{"CODEX_HOME": `C:/tools/codex/`}, want: Roots{
			Claude: defaults.Claude, Codex: `C:\tools\codex`, OpenCodeData: defaults.OpenCodeData, Cache: defaults.Cache, Config: defaults.Config, Data: defaults.Data,
		}},
		{name: "relative values ignored", env: map[string]string{
			"CLAUDE_CONFIG_DIR": `relative\claude`, "CODEX_HOME": `.\codex`, "XDG_DATA_HOME": "data", "LOCALAPPDATA": `..\local`, "APPDATA": "roaming",
		}, want: defaults},
		{name: "drive-less rooted values ignored", env: map[string]string{
			"CLAUDE_CONFIG_DIR": `\claude`, "CODEX_HOME": `\codex`, "LOCALAPPDATA": `\local`, "APPDATA": `\roaming`,
		}, want: defaults},
		{name: "drive-relative values ignored", env: map[string]string{"CODEX_HOME": `C:codex`}, want: defaults},
		{name: "empty values ignored", env: map[string]string{
			"CLAUDE_CONFIG_DIR": "", "CODEX_HOME": "", "XDG_DATA_HOME": "", "LOCALAPPDATA": "", "APPDATA": "",
		}, want: defaults},
		{name: "UNC override", env: map[string]string{"CLAUDE_CONFIG_DIR": `\\server\share\claude`}, want: Roots{
			Claude: `\\server\share\claude`, Codex: defaults.Codex, OpenCodeData: defaults.OpenCodeData, Cache: defaults.Cache, Config: defaults.Config, Data: defaults.Data,
		}},
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

func TestDefaultWindows(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME", "LOCALAPPDATA", "APPDATA"} {
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
