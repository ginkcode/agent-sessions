package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

// pidMatchesProcStart verifies the pid is alive with the expected start time.
// An empty procStart falls back to "pid exists", as does Windows, which has
// no comparable start time.
func (p *Provider) pidMatchesProcStart(pid int, procStart string) bool {
	if procStart == "" || runtime.GOOS == "windows" {
		return p.pidExists(pid)
	}
	expected, err := strconv.ParseInt(procStart, 10, 64)
	if err != nil {
		// Non-numeric procStart: fall back to pid existence.
		return p.pidExists(pid)
	}
	return expected > 0 && processStartTime(p.procFS, pid) == expected
}
