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

// HandoffDir returns the directory where full context markdown files are stored.
func HandoffDir(dataHome string) string {
	return filepath.Join(dataHome, "handoffs")
}

// SaveContextFile writes the untrimmed markdown to <dataHome>/handoffs/<sessionID>-full.md
// with 0600 permissions, and prunes context files older than 30 days.
func SaveContextFile(dataHome string, sessionID string, markdown string) (string, error) {
	dir := HandoffDir(dataHome)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("handoff: mkdir %s: %w", dir, err)
	}

	// Prune old files (> 30 days) asynchronously/opportunistically
	pruneOldFiles(dir, 30*24*time.Hour)

	filePath := filepath.Join(dir, sessionID+"-full.md")

	// Write atomically via 0600 temp file
	tmp, err := os.CreateTemp(dir, sessionID+"-*.tmp")
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
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("handoff: close %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, filePath); err != nil {
		return "", fmt.Errorf("handoff: rename %s -> %s: %w", tmpName, filePath, err)
	}

	return filePath, nil
}

func pruneOldFiles(dir string, maxAge time.Duration) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

// BuildLaunchCommand generates the shell command line to start the target agent
// with the prompt, changing directory to cwd first if specified.
func BuildLaunchCommand(target model.AgentID, prompt string, contextFilePath string, cwd string) string {
	actualPrompt := prompt
	if len(actualPrompt) > MaxPromptArgBytes && contextFilePath != "" {
		actualPrompt = fmt.Sprintf("Read %s completely, then continue with the engineering task.", contextFilePath)
	}

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
