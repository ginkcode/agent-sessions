package manage

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// windowsAgentServices are always-on helpers installed by agent desktop apps.
// They never host a session; a running Codex session still shows its own
// codex executable, which is checked like any other.
var windowsAgentServices = map[string]bool{
	// Service CodexSandboxService.OpenAI.Codex, run by services.exe whenever
	// the ChatGPT desktop app is installed.
	"codex-windows-sandbox-service": true,
}

// agentServer reports whether argv runs an agent's server/daemon subcommand
// (including editor-hosted ACP servers such as Zed's `opencode acp`). These
// host many sessions and are not themselves a session being edited in a
// terminal. Sessions they are actively using stay protected by the
// per-session recent-activity check. Shared by the Linux and Windows guards.
func agentServer(agent string, argv []string) bool {
	switch agent {
	case "codex":
		return hasArg(argv, "app-server")
	case "opencode":
		return hasArg(argv, "serve") || hasArg(argv, "acp")
	}
	return false
}

// classifyImages fails closed for script runtimes because an executable
// snapshot cannot establish which program a Node/Bun/Deno process is hosting.
// Codex and OpenCode executables mirror the Linux guard: args reads the
// arguments of only those processes so their server modes can be ignored.
// Arguments are matched, never returned or shown; unreadable ones block.
// Claude is not scanned: as on Linux, each Claude session is checked against
// Claude Code's own status files (the provider LiveFunc).
//
// The result maps an agent to the executable that makes it live, so the
// blocked reason can tell the user what to close.
func classifyImages(images map[int]string, args func(pid int, image string) ([]string, error)) (map[string]string, error) {
	if len(images) == 0 {
		return nil, ErrProcessUnknown
	}
	type match struct {
		pid        int
		image, exe string
		agent      string
	}
	var matches []match
	runtime := false
	for pid, image := range images {
		if image == "" {
			return nil, ErrProcessUnknown
		}
		name := strings.ToLower(image)
		if i := strings.LastIndexAny(name, `/\`); i >= 0 {
			name = name[i+1:]
		}
		exe := name
		name = strings.TrimSuffix(name, ".exe")
		if windowsAgentServices[name] {
			continue
		}
		switch name {
		case "node", "nodejs", "bun", "deno":
			runtime = true
			continue
		}
		for _, agent := range []string{"codex", "opencode"} {
			// Include platform/version-suffixed binaries (for example the
			// vendored codex-x86_64-pc-windows-msvc executable).
			if name == agent || strings.HasPrefix(name, agent+"-") {
				matches = append(matches, match{pid: pid, image: image, exe: exe, agent: agent})
			}
		}
	}
	if runtime {
		return nil, fmt.Errorf("%w: %w", ErrProcessUnknown, errRuntimeHost)
	}
	// Visit in a stable order so repeated scans give the same answer.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].exe != matches[j].exe {
			return matches[i].exe < matches[j].exe
		}
		return matches[i].pid < matches[j].pid
	})
	live := make(map[string]string)
	for _, m := range matches {
		if args == nil {
			return nil, ErrProcessUnknown
		}
		argv, err := args(m.pid, m.image)
		if err != nil || len(argv) == 0 {
			return nil, ErrProcessUnknown
		}
		if agentServer(m.agent, argv) {
			continue
		}
		if live[m.agent] == "" {
			live[m.agent] = m.exe
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
