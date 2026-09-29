package handoff

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// File suffixes name the handoff files this package owns; pruning and
// clearing never touch anything else in the directory. The prompt file holds
// the budgeted handoff the launch command points at; the context file holds
// the untrimmed history the prompt refers to.
const (
	promptFileSuffix  = "-handoff.md"
	contextFileSuffix = "-full.md"
)

// fileMaxAge is how long a handoff file is kept.
const fileMaxAge = 30 * 24 * time.Hour

// HandoffDir returns the directory where full context markdown files are stored.
func HandoffDir(dataHome string) string {
	return filepath.Join(dataHome, "handoffs")
}

// ContextFilePath returns where SaveContextFile writes the full context for
// sessionID.
func ContextFilePath(dataHome, sessionID string) (string, error) {
	return handoffFilePath(dataHome, sessionID, contextFileSuffix)
}

// PromptFilePath returns where SavePromptFile writes the handoff prompt for
// sessionID.
func PromptFilePath(dataHome, sessionID string) (string, error) {
	return handoffFilePath(dataHome, sessionID, promptFileSuffix)
}

// handoffFilePath reduces the ID to a safe file name: Claude subagent IDs
// contain "/", and no ID may steer the path outside the handoff directory.
func handoffFilePath(dataHome, sessionID, suffix string) (string, error) {
	if dataHome == "" {
		return "", fmt.Errorf("handoff: no data directory")
	}
	name := fileSafeID(sessionID)
	if name == "" {
		return "", fmt.Errorf("handoff: empty session id")
	}
	return filepath.Join(HandoffDir(dataHome), name+suffix), nil
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

// SaveContextFile writes the untrimmed markdown to ContextFilePath.
func SaveContextFile(dataHome string, sessionID string, markdown string) (string, error) {
	filePath, err := ContextFilePath(dataHome, sessionID)
	if err != nil {
		return "", err
	}
	return writeHandoffFile(filePath, markdown)
}

// SavePromptFile writes the handoff prompt to PromptFilePath.
func SavePromptFile(dataHome string, sessionID string, markdown string) (string, error) {
	filePath, err := PromptFilePath(dataHome, sessionID)
	if err != nil {
		return "", err
	}
	return writeHandoffFile(filePath, markdown)
}

// writeHandoffFile writes markdown with 0600 permissions (temp file, fsync,
// rename) and prunes handoff files older than 30 days.
func writeHandoffFile(filePath, markdown string) (string, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("handoff: mkdir %s: %w", dir, err)
	}

	pruneOldFiles(dir, fileMaxAge)

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

// pruneOldFiles removes this package's handoff files (and temp files a
// crashed write left behind) older than maxAge.
func pruneOldFiles(dir string, maxAge time.Duration) {
	now := time.Now()
	walkOwnFiles(dir, func(path string, info os.FileInfo) {
		if now.Sub(info.ModTime()) > maxAge {
			_ = os.Remove(path)
		}
	})
}

// FilesUsage reports how many handoff files exist and their total size.
func FilesUsage(dataHome string) (count int, bytes int64) {
	walkOwnFiles(HandoffDir(dataHome), func(_ string, info os.FileInfo) {
		count++
		bytes += info.Size()
	})
	return count, bytes
}

// ClearFiles removes every handoff file this package wrote and reports how
// many were removed and their total size. Other files are left alone.
func ClearFiles(dataHome string) (removed int, bytes int64, err error) {
	walkOwnFiles(HandoffDir(dataHome), func(path string, info os.FileInfo) {
		if rmErr := os.Remove(path); rmErr != nil {
			if err == nil {
				err = fmt.Errorf("handoff: remove %s: %w", path, rmErr)
			}
			return
		}
		removed++
		bytes += info.Size()
	})
	return removed, bytes, err
}

// walkOwnFiles calls fn for each regular file in dir that this package owns.
func walkOwnFiles(dir string, fn func(path string, info os.FileInfo)) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() {
			continue
		}
		ours := strings.HasSuffix(name, contextFileSuffix) ||
			strings.HasSuffix(name, promptFileSuffix) ||
			(strings.HasPrefix(name, ".handoff-") && strings.HasSuffix(name, ".tmp"))
		if !ours {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		fn(filepath.Join(dir, name), info)
	}
}

// LaunchPrompt returns the prompt the launch command carries: an instruction
// to read the prompt file, so the command stays one short line whatever the
// handoff size. Without a prompt file the prompt itself is passed.
func LaunchPrompt(prompt, promptFilePath string) string {
	if promptFilePath != "" {
		return fmt.Sprintf("Read %s completely, then continue the task it describes.", promptFilePath)
	}
	return prompt
}

// BuildLaunchCommand generates the shell command line to start the target agent
// with the prompt, changing directory to cwd first if specified.
func BuildLaunchCommand(target model.AgentID, prompt string, promptFilePath string, cwd string) string {
	actualPrompt := LaunchPrompt(prompt, promptFilePath)
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
