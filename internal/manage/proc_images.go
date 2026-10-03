package manage

import (
	"errors"
	"fmt"
	"strings"
)

// classifyImages fails closed for script runtimes because an executable-only
// snapshot cannot tell a background server from an interactive agent, nor
// establish which program a Node/Bun/Deno process is hosting.
//
// The result maps an agent to the executable that makes it live, so the
// blocked reason can tell the user what to close.
func classifyImages(images map[int]string) (map[string]string, error) {
	if len(images) == 0 {
		return nil, ErrProcessUnknown
	}
	live := make(map[string]string)
	for _, image := range images {
		if image == "" {
			return nil, ErrProcessUnknown
		}
		name := strings.ToLower(image)
		if i := strings.LastIndexAny(name, `/\`); i >= 0 {
			name = name[i+1:]
		}
		exe := name
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
				// Report the smallest name so repeated scans agree.
				if live[key] == "" || exe < live[key] {
					live[key] = exe
				}
			}
		}
	}
	return live, nil
}

func processSafetyError(live map[string]string, err error, agent string) error {
	if err != nil {
		// Only expose our fixed runtime-host explanation, never an arbitrary
		// process-table error that might contain user paths or command lines.
		if errors.Is(err, errRuntimeHost) {
			return fmt.Errorf("%w: %w", ErrProcessUnknown, errRuntimeHost)
		}
		return ErrProcessUnknown
	}
	if name := live[agent]; name != "" {
		return fmt.Errorf("%w: agent process is running: %s", ErrLive, name)
	}
	return nil
}
