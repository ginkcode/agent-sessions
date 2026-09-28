package manage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ProcFS abstracts /proc for the process scan, overridable in tests.
type ProcFS interface {
	Cmdlines() (map[int][]string, error) // pid -> argv
}

// LiveFunc reports whether a session (by agent and id) is currently live,
// or an error when liveness is unknown. Unknown must fail closed.
type LiveFunc func(ctx context.Context, agent string, id string) (bool, error)

// NowFunc returns the current time, overridable in tests.
type NowFunc func() time.Time

// recentWindow bounds how fresh a session's last activity may be before it
// counts as "too live to delete" even without a live process.
const recentWindow = 10 * time.Minute

// liveGuard refuses destructive action on sessions that are running,
// recently active, or whose liveness cannot be established.
type liveGuard struct {
	proc ProcFS
	now  NowFunc
}

func newLiveGuard(proc ProcFS) *liveGuard {
	return &liveGuard{proc: proc, now: defaultNow}
}

func defaultNow() time.Time { return time.Now() }

// procEntry is one running agent process.
type procEntry struct {
	agent string
	pid   int
}

// scanProcs finds running codex and opencode processes by reading
// /proc/*/cmdline. Daemon/server subcommands are excluded: they host many
// sessions and are not themselves a session being edited in a terminal.
func (g *liveGuard) scanProcs() (map[string]procEntry, error) {
	if g.proc == nil {
		return nil, errors.New("manage: process table unavailable")
	}
	cmdlines, err := g.proc.Cmdlines()
	if err != nil {
		return nil, errors.New("manage: cannot inspect process table")
	}
	out := make(map[string]procEntry)
	for pid, argv := range cmdlines {
		if len(argv) == 0 {
			continue
		}
		var bin string
		if len(argv) > 1 && isNodeLike(argv[0]) {
			bin = baseName(argv[1])
		} else {
			bin = baseName(argv[0])
		}
		agent := ""
		switch bin {
		case "codex":
			if hasArg(argv, "app-server") {
				continue
			}
			agent = "codex"
		case "opencode":
			if hasArg(argv, "serve") {
				continue
			}
			agent = "opencode"
		default:
			continue
		}
		out[fmt.Sprintf("%s:%d", agent, pid)] = procEntry{agent: agent, pid: pid}
	}
	return out, nil
}

func isNodeLike(argv0 string) bool {
	base := baseName(argv0)
	return base == "node" || base == "bun" || base == "deno"
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func hasArg(argv []string, want string) bool {
	for _, a := range argv[1:] {
		if a == want {
			return true
		}
	}
	return false
}

// procLive reports whether any codex or opencode process is currently running.
func (g *liveGuard) procLive(ctx context.Context) (map[string]bool, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	entries, err := g.scanProcs()
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool)
	for _, e := range entries {
		out[e.agent] = true
	}
	return out, nil
}

// procDirFS reads /proc via os.ReadDir on Linux. Overridden in tests.
type procDirFS struct {
	dir string
}

// Cmdlines returns pid -> argv for readable /proc entries.
func (p procDirFS) Cmdlines() (map[int][]string, error) {
	dir := p.dir
	if dir == "" {
		dir = "/proc"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make(map[int][]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		cmdlineFile := dir + "/" + entry.Name() + "/cmdline"
		data, err := os.ReadFile(cmdlineFile)
		if err != nil {
			continue // gone, or owned by another user
		}
		var argv []string
		for _, part := range strings.Split(string(data), "\x00") {
			if part != "" {
				argv = append(argv, part)
			}
		}
		if len(argv) > 0 {
			out[pid] = argv
		}
	}
	return out, nil
}
