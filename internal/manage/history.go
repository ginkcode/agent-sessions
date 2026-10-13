package manage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// errHistoryChanged means the history file changed while it was rewritten.
var errHistoryChanged = errors.New("history file changed during cleanup")

// promptHistory is an agent's prompt history file: every prompt typed, for
// up-arrow recall, each line naming its session in field. Deleting a
// session with the agent's own tools leaves its lines there.
type promptHistory struct {
	agent model.AgentID
	name  string
	path  string
	field string
	// stopped fails unless no process of the agent is running.
	stopped func(context.Context) error
}

func (m *Manager) promptHistories() []promptHistory {
	var out []promptHistory
	if m.claude != nil {
		out = append(out, promptHistory{model.AgentClaude, "Claude Code", filepath.Join(m.claude.root, "history.jsonl"), "sessionId", m.claudeStopped})
	}
	if m.codex != nil {
		out = append(out, promptHistory{model.AgentCodex, "Codex", filepath.Join(m.codex.root, "history.jsonl"), "session_id", func(ctx context.Context) error {
			live, err := m.procLiveMap(ctx)
			return processSafetyError(live, err, string(model.AgentCodex))
		}})
	}
	return out
}

// claudeStopped uses Claude Code's own session status files, as the
// per-session liveness check does: the process table cannot tell a Claude
// process hosted by Node from any other Node program.
func (m *Manager) claudeStopped(ctx context.Context) error {
	if m.claudeRunning == nil {
		return ErrProcessUnknown
	}
	running, err := m.claudeRunning(ctx)
	if err != nil {
		return ErrProcessUnknown
	}
	if running {
		return ErrLive
	}
	return nil
}

// pruneHistories removes the forgotten sessions' prompts from each agent's
// prompt history. The agent rewrites or appends to the file while it runs,
// so a running agent's history is left alone; the returned notes say so.
func (m *Manager) pruneHistories(ctx context.Context, forgotten []model.SessionRef) []string {
	var notes []string
	for _, h := range m.promptHistories() {
		ids := map[string]bool{}
		for _, ref := range forgotten {
			// Subagents (Claude "<id>/agent-<x>") have no history lines.
			if ref.Agent == h.agent && !strings.Contains(ref.ID, "/") {
				ids[ref.ID] = true
			}
		}
		if len(ids) == 0 {
			continue
		}
		if err := h.stopped(ctx); err != nil {
			notes = append(notes, fmt.Sprintf("%s prompt history still lists the deleted sessions: %s is running or could not be checked.", h.name, h.name))
			continue
		}
		err := pruneHistoryFile(h.path, h.field, ids)
		if errors.Is(err, errHistoryChanged) {
			err = pruneHistoryFile(h.path, h.field, ids)
		}
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s prompt history could not be cleaned.", h.name))
		}
	}
	return notes
}

// pruneHistoryFile drops the JSONL lines whose field is one of ids and
// atomically replaces the file, keeping its mode. Lines that are not JSON
// objects are kept as they are. The file is left untouched when nothing
// matches, and errHistoryChanged is returned when it changed meanwhile.
func pruneHistoryFile(path, field string, ids map[string]bool) error {
	before, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !before.Mode().IsRegular() {
		return errors.New("history is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var kept bytes.Buffer
	kept.Grow(len(data))
	removed := false
	for rest := data; len(rest) > 0; {
		line := rest
		if i := bytes.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i+1], rest[i+1:]
		} else {
			rest = nil
		}
		var rec map[string]json.RawMessage
		var id string
		if json.Unmarshal(line, &rec) == nil && json.Unmarshal(rec[field], &id) == nil && ids[id] {
			removed = true
			continue
		}
		kept.Write(line)
	}
	if !removed {
		return nil
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".history-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(before.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(kept.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Do not drop a line appended since the read.
	after, err := os.Lstat(path)
	if err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return errHistoryChanged
	}
	return os.Rename(tmpPath, path)
}
