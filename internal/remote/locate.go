package remote

import (
	"fmt"
	"os"
	"path/filepath"
)

// LocateServer finds the bundled headless server archive for the given OS and architecture.
// It never relies on the current working directory, only on executable location and fixed paths.
func LocateServer(targetOS, targetArch string) (string, error) {
	filename := fmt.Sprintf("agent-sessions-cli-%s-%s.gz", targetOS, targetArch)

	var candidates []string

	// 1. Explicit env override (for tests and development)
	if envDir := os.Getenv("AGENT_SESSIONS_REMOTE_SERVERS_DIR"); envDir != "" {
		candidates = append(candidates, filepath.Join(envDir, filename))
	}

	// 2. Paths relative to the running executable
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			// Same dir / remote
			filepath.Join(exeDir, "remote", filename),
			// macOS App Bundle (Contents/MacOS/agent-sessions -> Contents/Resources/remote)
			filepath.Join(exeDir, "..", "Resources", "remote", filename),
			// Adjacent remote
			filepath.Join(exeDir, "..", "remote", filename),
			// Development build directory
			filepath.Join(exeDir, "..", "build", "remote", filename),
			filepath.Join(exeDir, "build", "remote", filename),
		)
	}

	// 3. System install locations on Linux
	candidates = append(candidates,
		filepath.Join("/usr/lib/agent-sessions/remote", filename),
		filepath.Join("/usr/local/lib/agent-sessions/remote", filename),
	)

	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}

	return "", fmt.Errorf("could not find bundled server %s (searched %v)", filename, candidates)
}
