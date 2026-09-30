package remote

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ErrServerBundleNotFound means this app has no server archive for the
// remote platform. Retrying cannot fix it.
var ErrServerBundleNotFound = errors.New("no bundled server")

// LocateServer finds the bundled headless server archive for the given OS and architecture.
// It never relies on the current working directory, only on executable location and fixed paths.
func LocateServer(targetOS, targetArch string) (string, error) {
	filename := fmt.Sprintf("agent-sessions-cli-%s-%s.gz", targetOS, targetArch)

	var candidates []string

	// 1. Explicit env override (for tests and development)
	if envDir := os.Getenv("AGENT_SESSIONS_REMOTE_SERVERS_DIR"); envDir != "" {
		candidates = append(candidates, filepath.Join(envDir, filename))
	}

	// 2. Dev tree. `wails dev` runs a binary outside the repo, so walk up
	// from this source file to go.mod and look in build/remote.
	if root := moduleRoot(); root != "" {
		candidates = append(candidates, filepath.Join(root, "build", "remote", filename))
	}

	// 3. Paths relative to the running executable
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

	// 4. System install locations on Linux
	candidates = append(candidates,
		filepath.Join("/usr/lib/agent-sessions/remote", filename),
		filepath.Join("/usr/local/lib/agent-sessions/remote", filename),
	)

	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}

	return "", fmt.Errorf("%w for %s/%s (%s); this build cannot deploy to that host", ErrServerBundleNotFound, targetOS, targetArch, filename)
}

// moduleRoot walks up from this source file to the directory containing go.mod.
// `go run` and `wails dev` keep the source path, so a dev build finds
// build/remote without an env override. A released binary has no source path
// and returns "" (or a directory that simply has no bundle).
func moduleRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	// -trimpath records a module-relative path; resolving it would depend on
	// the working directory.
	if !filepath.IsAbs(file) {
		return ""
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
