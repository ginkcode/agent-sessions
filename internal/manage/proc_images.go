package manage

import (
	"errors"
	"fmt"
	"strings"
)

// classifyImages fails closed for script runtimes because an executable-only
// snapshot cannot tell a background server from an interactive agent, nor
// establish which program a Node/Bun/Deno process is hosting.
func classifyImages(images map[int]string) (map[string]bool, error) {
	if len(images) == 0 {
		return nil, ErrProcessUnknown
	}
	live := make(map[string]bool)
	for _, image := range images {
		if image == "" {
			return nil, ErrProcessUnknown
		}
		name := strings.ToLower(image)
		if i := strings.LastIndexAny(name, `/\`); i >= 0 {
			name = name[i+1:]
		}
		name = strings.TrimSuffix(name, ".exe")
		switch name {
		case "node", "nodejs", "bun", "deno":
			return nil, fmt.Errorf("%w: %w", ErrProcessUnknown, errRuntimeHost)
		}
		for _, agent := range []string{"claude", "codex", "opencode"} {
			// Include platform/version-suffixed binaries (for example the
			// vendored codex-x86_64-pc-windows-msvc executable).
			if name == agent || strings.HasPrefix(name, agent+"-") {
				key := agent
				if agent == "claude" {
					key = "claude-code"
				}
				live[key] = true
			}
		}
	}
	return live, nil
}

func processSafetyError(live map[string]bool, err error, agent string) error {
	if err != nil {
		// Only expose our fixed runtime-host explanation, never an arbitrary
		// process-table error that might contain user paths or command lines.
		if errors.Is(err, errRuntimeHost) {
			return fmt.Errorf("%w: %w", ErrProcessUnknown, errRuntimeHost)
		}
		return ErrProcessUnknown
	}
	if live[agent] {
		return fmt.Errorf("%w: agent process is running", ErrLive)
	}
	return nil
}
