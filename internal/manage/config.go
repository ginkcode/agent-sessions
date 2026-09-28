package manage

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config holds the user's opt-in settings for destructive session management.
// Persisted in ~/.config/agent-sessions/config.toml under the [manage] table.
type Config struct {
	Enabled              bool `json:"enabled"`
	AllowPermanentDelete bool `json:"allowPermanentDelete"`
}

// ConfigStore loads and updates the manage settings from a TOML file.
type ConfigStore struct {
	path string
	mu   sync.RWMutex
}

// NewConfigStore creates a store bound to configPath (typically
// <roots.Config>/config.toml).
func NewConfigStore(configPath string) *ConfigStore {
	return &ConfigStore{path: configPath}
}

// Path returns the config file path.
func (cs *ConfigStore) Path() string {
	return cs.path
}

// Load reads the current configuration. If the file does not exist, safe
// defaults (all features disabled) are returned.
func (cs *ConfigStore) Load() (Config, error) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return loadConfig(cs.path)
}

// Save atomically writes cfg to disk, preserving comments and other tables
// in the existing file.
func (cs *ConfigStore) Save(cfg Config) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return saveConfig(cs.path, cfg)
}

func loadConfig(path string) (Config, error) {
	var cfg Config
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("manage: read config: %w", err)
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	inManage := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.TrimSpace(line[1 : len(line)-1])
			inManage = (section == "manage")
			continue
		}
		if !inManage {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		// Strip trailing comments
		if idx := strings.IndexAny(val, "#;"); idx != -1 {
			val = strings.TrimSpace(val[:idx])
		}
		val = strings.Trim(val, `"'`)

		switch key {
		case "enabled":
			cfg.Enabled = parseBool(val)
		case "allow_permanent_delete":
			cfg.AllowPermanentDelete = parseBool(val)
		}
	}
	if err := scanner.Err(); err != nil {
		return cfg, fmt.Errorf("manage: parse config: %w", err)
	}
	return cfg, nil
}

func parseBool(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "true" || s == "1" || s == "yes" || s == "on"
}

func saveConfig(path string, cfg Config) error {
	if path == "" {
		return fmt.Errorf("manage: empty config path")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("manage: create config dir: %w", err)
	}
	// Enforce 0700 on existing directory as well
	_ = os.Chmod(dir, 0o700)

	var existing []byte
	if data, err := os.ReadFile(path); err == nil {
		existing = data
	}

	updated := updateManageSection(existing, cfg)

	tmp, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		return fmt.Errorf("manage: create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("manage: chmod temp config: %w", err)
	}

	if _, err := tmp.Write(updated); err != nil {
		return fmt.Errorf("manage: write temp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("manage: sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("manage: close temp config: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("manage: replace config: %w", err)
	}
	return nil
}

func updateManageSection(data []byte, cfg Config) []byte {
	if len(data) == 0 {
		var buf bytes.Buffer
		buf.WriteString("[manage]\n")
		fmt.Fprintf(&buf, "enabled = %t\n", cfg.Enabled)
		fmt.Fprintf(&buf, "allow_permanent_delete = %t\n", cfg.AllowPermanentDelete)
		return buf.Bytes()
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	var out bytes.Buffer

	inManage := false
	manageSeen := false
	enabledWritten := false
	permWritten := false

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inManage {
				// Leaving [manage] section; write any missing keys
				if !enabledWritten {
					fmt.Fprintf(&out, "enabled = %t\n", cfg.Enabled)
				}
				if !permWritten {
					fmt.Fprintf(&out, "allow_permanent_delete = %t\n", cfg.AllowPermanentDelete)
				}
				inManage = false
			}
			section := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			if section == "manage" {
				inManage = true
				manageSeen = true
				out.WriteString(line)
				out.WriteByte('\n')
				continue
			}
		}

		if inManage {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				switch key {
				case "enabled":
					fmt.Fprintf(&out, "enabled = %t\n", cfg.Enabled)
					enabledWritten = true
					continue
				case "allow_permanent_delete":
					fmt.Fprintf(&out, "allow_permanent_delete = %t\n", cfg.AllowPermanentDelete)
					permWritten = true
					continue
				}
			}
		}

		out.WriteString(line)
		out.WriteByte('\n')
	}

	if inManage {
		if !enabledWritten {
			fmt.Fprintf(&out, "enabled = %t\n", cfg.Enabled)
		}
		if !permWritten {
			fmt.Fprintf(&out, "allow_permanent_delete = %t\n", cfg.AllowPermanentDelete)
		}
	} else if !manageSeen {
		if out.Len() > 0 && !bytes.HasSuffix(out.Bytes(), []byte("\n\n")) {
			out.WriteByte('\n')
		}
		out.WriteString("[manage]\n")
		fmt.Fprintf(&out, "enabled = %t\n", cfg.Enabled)
		fmt.Fprintf(&out, "allow_permanent_delete = %t\n", cfg.AllowPermanentDelete)
	}

	return out.Bytes()
}
