//go:build darwin

package paths

import (
	"path/filepath"
)

// FromEnv resolves roots using the provided getenv function and user home directory.
// Relative or empty environment variable values are ignored.
func FromEnv(getenv func(string) string, home string) Roots {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

	toolRoot := func(env, fallback string) string {
		if v := getenv(env); filepath.IsAbs(v) {
			return filepath.Clean(v)
		}
		return filepath.Join(home, fallback)
	}

	dataHome := getenv("XDG_DATA_HOME")
	dataBase := filepath.Join(home, ".local", "share")
	if filepath.IsAbs(dataHome) {
		dataBase = filepath.Clean(dataHome)
	}

	// TODO(M5-01): finalize Darwin cache/config locations.
	return Roots{
		Claude:       toolRoot("CLAUDE_CONFIG_DIR", ".claude"),
		Codex:        toolRoot("CODEX_HOME", ".codex"),
		OpenCodeData: filepath.Join(dataBase, "opencode"),
		Cache:        filepath.Join(home, "Library", "Caches", "agent-sessions"),
		Config:       filepath.Join(home, "Library", "Application Support", "agent-sessions"),
	}
}
