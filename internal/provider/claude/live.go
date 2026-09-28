package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ginkcode/agent-sessions/internal/provider"
)

type liveSession struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	ProcStart string `json:"procStart"`
	Status    string `json:"status"`
	UpdatedAt int64  `json:"updatedAt"` // unix millis
}

// Live returns a map of session IDs that are actively running, keyed by
// session ID. On Linux, a session is live when its PID exists in procfs
// and the process start time (field 22 of /proc/<pid>/stat) matches
// procStart, guarding against PID reuse.
func (p *Provider) Live(ctx context.Context) (map[string]provider.LiveInfo, error) {
	out := make(map[string]provider.LiveInfo)

	files, err := os.ReadDir(filepath.Join(p.root, "sessions"))
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}

	for _, f := range files {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
		name := f.Name()
		if f.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".key") {
			continue
		}
		path := filepath.Join(p.root, "sessions", name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var ls liveSession
		if err := json.Unmarshal(data, &ls); err != nil {
			continue
		}
		if ls.PID <= 0 || ls.SessionID == "" {
			continue
		}
		alive := p.pidMatchesProcStart(ls.PID, ls.ProcStart)
		if !alive {
			continue
		}
		out[ls.SessionID] = provider.LiveInfo{
			PID:       ls.PID,
			Status:    ls.Status,
			UpdatedAt: time.UnixMilli(ls.UpdatedAt),
		}
	}

	return out, nil
}

// processStartTime reads /proc/<pid>/stat and returns field 22 (starttime).
// The comm field may contain spaces and parentheses, so we split after the
// last ')'. Returns 0 when the process does not exist or the field is absent.
func processStartTime(procFS string, pid int) int64 {
	data, err := os.ReadFile(filepath.Join(procFS, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	s := string(data)
	idx := strings.LastIndex(s, ")")
	if idx < 0 || idx+2 > len(s) {
		return 0
	}
	fields := strings.Fields(s[idx+2:]) // fields[0] is state (field 3)
	if len(fields) < 20 {
		return 0
	}
	starttime, err := strconv.ParseInt(fields[19], 10, 64) // field 22
	if err != nil {
		return 0
	}
	return starttime
}

// pidMatchesProcStart verifies the pid is alive with the expected start time.
// An empty procStart falls back to "pid exists".
func (p *Provider) pidMatchesProcStart(pid int, procStart string) bool {
	if procStart == "" {
		return p.pidExists(pid)
	}
	expected, err := strconv.ParseInt(procStart, 10, 64)
	if err != nil {
		// Non-numeric procStart: fall back to pid existence.
		return p.pidExists(pid)
	}
	return expected > 0 && processStartTime(p.procFS, pid) == expected
}

// pidExists reports whether a process with the given pid exists.
func (p *Provider) pidExists(pid int) bool {
	_, err := os.Stat(filepath.Join(p.procFS, strconv.Itoa(pid)))
	return err == nil
}
