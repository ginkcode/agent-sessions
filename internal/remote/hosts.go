package remote

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// HostAlias represents a configured SSH host target.
type HostAlias struct {
	Name     string `json:"name"`
	HostName string `json:"hostName,omitempty"`
	User     string `json:"user,omitempty"`
	Port     int    `json:"port,omitempty"`
}

// Sentinel errors for host validation and parsing.
var (
	ErrInvalidHostAlias = errors.New("invalid host alias")
	ErrHostNotFound     = errors.New("host alias not found")
)

// DefaultConfigPath returns the path to the SSH config file, respecting AGENT_SESSIONS_SSH_CONFIG.
func DefaultConfigPath() string {
	if p := os.Getenv("AGENT_SESSIONS_SSH_CONFIG"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "config")
}

// ValidateHostAlias checks that an alias does not start with '-' and contains no
// whitespace, quotes, or dangerous shell characters.
func ValidateHostAlias(alias string) error {
	if alias == "" {
		return fmt.Errorf("%w: cannot be empty", ErrInvalidHostAlias)
	}
	if strings.HasPrefix(alias, "-") {
		return fmt.Errorf("%w: cannot begin with '-'", ErrInvalidHostAlias)
	}
	for _, r := range alias {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%w: contains whitespace or control character", ErrInvalidHostAlias)
		}
		if strings.ContainsRune(";&|`$'\"><\\(){}[]*?!#", r) {
			return fmt.Errorf("%w: contains invalid character %q", ErrInvalidHostAlias, r)
		}
	}
	return nil
}

// ListConfigHosts parses the default SSH configuration file and returns valid host aliases.
func ListConfigHosts() ([]HostAlias, error) {
	cfgPath := DefaultConfigPath()
	if cfgPath == "" {
		return nil, nil
	}
	return ParseConfigFile(cfgPath)
}

// ParseConfigFile parses an SSH config file and any files included via Include directives.
func ParseConfigFile(path string) ([]HostAlias, error) {
	visited := make(map[string]bool)
	home, _ := os.UserHomeDir()
	sshDir := filepath.Dir(path)
	if home != "" && sshDir == "." {
		sshDir = filepath.Join(home, ".ssh")
	}

	aliases, err := parseConfigRecursive(path, sshDir, home, visited)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return aliases, nil
}

func parseConfigRecursive(path, sshDir, home string, visited map[string]bool) ([]HostAlias, error) {
	cleanPath := filepath.Clean(path)
	if visited[cleanPath] {
		return nil, nil
	}
	visited[cleanPath] = true

	f, err := os.Open(cleanPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var result []HostAlias
	seen := make(map[string]int)

	scanner := bufio.NewScanner(f)
	var currentAliases []int

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Strip trailing comments (space followed by #)
		if idx := strings.Index(line, " #"); idx != -1 {
			line = strings.TrimSpace(line[:idx])
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		keyword := strings.ToLower(fields[0])
		args := fields[1:]

		switch keyword {
		case "include":
			for _, pattern := range args {
				// Expand ~ or relative paths
				expanded := expandPath(pattern, sshDir, home)
				matches, err := filepath.Glob(expanded)
				if err != nil || len(matches) == 0 {
					continue
				}
				for _, match := range matches {
					incAliases, err := parseConfigRecursive(match, sshDir, home, visited)
					if err == nil {
						for _, a := range incAliases {
							if _, exists := seen[a.Name]; !exists {
								seen[a.Name] = len(result)
								result = append(result, a)
							}
						}
					}
				}
			}

		case "host":
			currentAliases = nil
			for _, pat := range args {
				// Skip wildcards and negations, and the wsl: targets the
				// app reserves for WSL distributions.
				if strings.ContainsAny(pat, "*?") || strings.HasPrefix(pat, "!") || strings.HasPrefix(pat, WSLPrefix) {
					continue
				}
				if err := ValidateHostAlias(pat); err != nil {
					continue
				}
				if idx, exists := seen[pat]; exists {
					currentAliases = append(currentAliases, idx)
				} else {
					alias := HostAlias{Name: pat}
					idx := len(result)
					seen[pat] = idx
					result = append(result, alias)
					currentAliases = append(currentAliases, idx)
				}
			}

		case "hostname":
			if len(args) > 0 {
				for _, idx := range currentAliases {
					if result[idx].HostName == "" {
						result[idx].HostName = args[0]
					}
				}
			}

		case "user":
			if len(args) > 0 {
				for _, idx := range currentAliases {
					if result[idx].User == "" {
						result[idx].User = args[0]
					}
				}
			}

		case "port":
			if len(args) > 0 {
				if p, err := strconv.Atoi(args[0]); err == nil && p > 0 {
					for _, idx := range currentAliases {
						if result[idx].Port == 0 {
							result[idx].Port = p
						}
					}
				}
			}
		}
	}

	return result, scanner.Err()
}

func expandPath(pattern, sshDir, home string) string {
	if strings.HasPrefix(pattern, "~/") && home != "" {
		return filepath.Join(home, pattern[2:])
	}
	if filepath.IsAbs(pattern) {
		return pattern
	}
	return filepath.Join(sshDir, pattern)
}

// ResolveHost resolves effective SSH options for alias by running `ssh -G`.
// If configFile is non-empty, it adds `-F <configFile>`.
func ResolveHost(ctx context.Context, sshBin string, alias string, configFile string) (*HostAlias, error) {
	if err := ValidateHostAlias(alias); err != nil {
		return nil, err
	}
	var args []string
	if configFile != "" {
		args = append(args, "-F", configFile)
	}
	args = append(args, "-G", "--", alias)

	cmd, err := newSSHCommand(ctx, sshBin, args...)
	if err != nil {
		return nil, err
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ssh -G failed: %w", err)
	}

	res := &HostAlias{Name: alias}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "hostname":
			res.HostName = val
		case "user":
			res.User = val
		case "port":
			if p, err := strconv.Atoi(val); err == nil && p > 0 {
				res.Port = p
			}
		}
	}
	return res, nil
}
