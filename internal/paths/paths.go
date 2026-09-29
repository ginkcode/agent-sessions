// Package paths locates the per-provider data and application directories.
package paths

import (
	"fmt"
	"os"
)

// Roots holds the default data roots for providers and the application's own directories.
type Roots struct {
	Claude       string
	Codex        string
	OpenCodeData string
	Cache        string
	Config       string
	Data         string
}

// Default resolves roots from the process environment and the current user's home.
func Default() (Roots, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Roots{}, fmt.Errorf("paths: user home directory: %w", err)
	}
	return FromEnv(os.Getenv, home), nil
}
