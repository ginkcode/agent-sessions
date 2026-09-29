package handoff

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// MaxPromptArgBytes is the prompt length ceiling for CLI argument delivery (120 KiB).
// Beyond this, the command instructs the agent to read the full context file.
const MaxPromptArgBytes = 120 * 1024

// contextFileSuffix names the full-context files this package owns; pruning
// never touches anything else in the directory.
const contextFileSuffix = "-full.md"

// contextFileMaxAge is how long a full-context file is kept.
const contextFileMaxAge = 30 * 24 * time.Hour

// HandoffDir returns the directory where full context markdown files are stored.
func HandoffDir(dataHome string) string {
	return filepath.Join(dataHome, "handoffs")
}

// ContextFilePath returns where SaveContextFile writes the full context for
// sessionID. The ID is reduced to a safe file name: Claude subagent IDs
// contain "/", and no ID may steer the path outside the handoff directory.
func ContextFilePath(dataHome, sessionID string) (string, error) {
	if dataHome == "" {
		return "", fmt.Errorf("handoff: no data directory")
	}
	name := fileSafeID(sessionID)
	if name == "" {
		return "", fmt.Errorf("handoff: empty session id")
	}
	return filepath.Join(HandoffDir(dataHome), name+contextFileSuffix), nil
}

func fileSafeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
		if b.Len() >= 128 {
			break
		}
	}
	return strings.Trim(b.String(), "_")
}

// SaveContextFile writes the untrimmed markdown to ContextFilePath with 0600
// permissions (temp file, fsync, rename) and prunes context files older than
// 30 days.
func SaveContextFile(dataHome string, sessionID string, markdown string) (string, error) {
	filePath, err := ContextFilePath(dataHome, sessionID)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("handoff: mkdir %s: %w", dir, err)
	}

	pruneOldFiles(dir, contextFileMaxAge)

	tmp, err := os.CreateTemp(dir, ".handoff-*.tmp")
	if err != nil {
		return "", fmt.Errorf("handoff: create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return "", fmt.Errorf("handoff: chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.WriteString(markdown); err != nil {
		return "", fmt.Errorf("handoff: write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("handoff: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("handoff: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, filePath); err != nil {
		return "", fmt.Errorf("handoff: rename %s -> %s: %w", tmpName, filePath, err)
	}
	return filePath, nil
}

// pruneOldFiles removes this package's context files (and temp files a
// crashed write left behind) older than maxAge.
func pruneOldFiles(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() {
			continue
		}
		ours := strings.HasSuffix(name, contextFileSuffix) ||
			(strings.HasPrefix(name, ".handoff-") && strings.HasSuffix(name, ".tmp"))
		if !ours {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// LaunchPrompt returns the prompt the launch command carries. Above
// MaxPromptArgBytes, and only when a context file exists to point at, it is
// replaced by an instruction to read that file; pointer reports the switch.
func LaunchPrompt(prompt, contextFilePath string) (text string, pointer bool) {
	if len(prompt) > MaxPromptArgBytes && contextFilePath != "" {
		return fmt.Sprintf("Read %s completely, then continue with the engineering task.", contextFilePath), true
	}
	return prompt, false
}

// BuildLaunchCommand generates the shell command line to start the target agent
// with the prompt, changing directory to cwd first if specified.
func BuildLaunchCommand(target model.AgentID, prompt string, contextFilePath string, cwd string) string {
	actualPrompt, _ := LaunchPrompt(prompt, contextFilePath)
	argv := LaunchArgv(target, actualPrompt)
	cmdStr := joinCommand(argv)
	if cwd == "" {
		return cmdStr
	}
	return fmt.Sprintf("cd %s && %s", shellEscape(cwd), cmdStr)
}

// LaunchArgv returns the CLI argv for invoking the target agent with the prompt.
func LaunchArgv(target model.AgentID, prompt string) []string {
	switch target {
	case model.AgentClaude:
		return []string{"claude", prompt}
	case model.AgentCodex:
		return []string{"codex", prompt}
	case model.AgentOpenCode:
		return []string{"opencode", "--prompt", prompt}
	default:
		return []string{string(target), prompt}
	}
}

// shellEscape quotes a single argument for POSIX shells.
func shellEscape(arg string) string {
	if arg == "" {
		return "''"
	}
	if safeUnquoted(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
}

func safeUnquoted(arg string) bool {
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '=' || r == '@' || r == '+' || r == ',':
		default:
			return false
		}
	}
	return true
}

func joinCommand(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, shellEscape(arg))
	}
	return strings.Join(parts, " ")
}
