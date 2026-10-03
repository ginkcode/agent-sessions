package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// windowsCWDCases are synthetic Windows working directories as Claude Code
// on Windows records them, with the CWD every host should report. None of
// them exist; the drive root is the only one that may on a Windows host.
var windowsCWDCases = []struct {
	name, raw, want string
	mayExist        bool
}{
	{name: "drive", raw: `C:\agent-sessions-synthetic\Proj`, want: `C:\agent-sessions-synthetic\Proj`},
	{name: "lower drive forward slashes", raw: `c:/agent-sessions-synthetic/Proj/`, want: `C:\agent-sessions-synthetic\Proj`},
	{name: "mixed separators and dots", raw: `C:\agent-sessions-synthetic/a\..\Proj\.\`, want: `C:\agent-sessions-synthetic\Proj`},
	{name: "drive root", raw: `C:\`, want: `C:\`, mayExist: true},
	{name: "long path prefix", raw: `\\?\D:\agent-sessions-synthetic\x\\`, want: `D:\agent-sessions-synthetic\x`},
	{name: "long path unc", raw: `\\?\UNC\localhost\agent-sessions-missing\repo`, want: `\\localhost\agent-sessions-missing\repo`},
	{name: "unc trailing separator", raw: `\\localhost\agent-sessions-missing\repo\`, want: `\\localhost\agent-sessions-missing\repo`},
	{name: "unc share root", raw: `\\localhost\agent-sessions-missing\`, want: `\\localhost\agent-sessions-missing`},
}

// writeWindowsTranscript writes a synthetic two-record transcript whose cwd
// is quoted by json.Marshal, as Claude Code writes Windows paths.
func writeWindowsTranscript(t *testing.T, id, cwd string) (root, path string) {
	t.Helper()
	root = t.TempDir()
	project := filepath.Join(root, "projects", "C--agent-sessions-synthetic")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(project, id+".jsonl")
	writeWindowsRecords(t, path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY,
		map[string]any{
			"type": "user", "uuid": "u1", "sessionId": id, "timestamp": "2026-09-20T10:00:00Z",
			"cwd": cwd, "version": "2.1.0",
			"message": map[string]any{"role": "user", "content": "Fix the Windows build"},
		},
		map[string]any{
			"type": "assistant", "uuid": "a1", "sessionId": id, "timestamp": "2026-09-20T10:00:05Z",
			"cwd": cwd,
			"message": map[string]any{
				"id": "msg-a1", "role": "assistant", "model": "claude-sonnet-5",
				"content": []any{map[string]any{"type": "text", "text": "Done."}},
				"usage":   map[string]any{"input_tokens": 10, "output_tokens": 2},
			},
		},
	)
	return root, path
}

func writeWindowsRecords(t *testing.T, path string, flag int, records ...map[string]any) {
	t.Helper()
	var b strings.Builder
	for _, rec := range records {
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	f, err := os.OpenFile(path, flag, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsCWDScanAndLoad(t *testing.T) {
	t.Parallel()
	for _, tc := range windowsCWDCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const id = "win-session"
			root, path := writeWindowsTranscript(t, id, tc.raw)
			p := New(root, nil)

			first, err := p.Scan(context.Background(), provider.ScanState{})
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Changed) != 1 {
				t.Fatalf("Changed = %d, want 1", len(first.Changed))
			}
			meta := first.Changed[0]
			if meta.CWD != tc.want {
				t.Errorf("scan CWD = %q, want %q", meta.CWD, tc.want)
			}
			// A foreign path never exists here; on Windows only the drive
			// root may.
			if !tc.mayExist || runtime.GOOS != "windows" {
				if !meta.CWDMissing {
					t.Errorf("scan CWDMissing = false for %q", meta.CWD)
				}
			}
			if runtime.GOOS != "windows" && meta.RepoRoot != "" {
				t.Errorf("scan RepoRoot = %q for a foreign path", meta.RepoRoot)
			}

			tx, err := p.Load(context.Background(), model.SessionRef{Agent: model.AgentClaude, ID: id})
			if err != nil {
				t.Fatal(err)
			}
			if tx.Meta.CWD != tc.want || tx.Meta.CWDMissing != meta.CWDMissing {
				t.Errorf("load CWD = %q (missing %v), want %q (missing %v)", tx.Meta.CWD, tx.Meta.CWDMissing, tc.want, meta.CWDMissing)
			}

			// Appending resumes from the checkpoint, which already holds the
			// normalized CWD.
			writeWindowsRecords(t, path, os.O_APPEND|os.O_WRONLY, map[string]any{
				"type": "user", "uuid": "u2", "sessionId": id, "timestamp": "2026-09-20T10:01:00Z",
				"cwd":     tc.raw,
				"message": map[string]any{"role": "user", "content": "And the tests"},
			})
			resumed, err := p.Scan(context.Background(), first.State)
			if err != nil {
				t.Fatal(err)
			}
			if len(resumed.Changed) != 1 || resumed.Changed[0].CWD != tc.want {
				t.Errorf("resumed scan = %+v, want CWD %q", resumed.Changed, tc.want)
			}
		})
	}
}

// Checkpoints written before checkpointVersion 4 hold CWDs normalized by the
// old rule, which left Windows paths raw on other hosts. The upgrade rescans
// even unchanged transcripts so the catalog gets the normalized CWD, and
// still reports transcripts deleted since.
func TestWindowsCWDOldCheckpointRescanned(t *testing.T) {
	t.Parallel()
	const raw, want = `c:/agent-sessions-synthetic/Proj/`, `C:\agent-sessions-synthetic\Proj`
	root, path := writeWindowsTranscript(t, "kept", raw)
	gone := filepath.Join(filepath.Dir(path), "gone.jsonl")
	writeWindowsRecords(t, gone, os.O_CREATE|os.O_WRONLY, map[string]any{
		"type": "user", "uuid": "g1", "sessionId": "gone", "timestamp": "2026-09-20T09:00:00Z",
		"cwd": raw, "message": map[string]any{"role": "user", "content": "Old work"},
	})
	p := New(root, nil)
	first, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil || len(first.Changed) != 2 {
		t.Fatalf("first scan = %+v, %v", first.Changed, err)
	}

	// Rewrite both checkpoints as version 3 wrote them.
	for src, state := range first.State.Sources {
		var cp map[string]any
		if err := json.Unmarshal(state.Checkpoint, &cp); err != nil {
			t.Fatal(err)
		}
		cp["version"] = 3
		cp["meta"].(map[string]any)["cwd"] = raw
		if state.Checkpoint, err = json.Marshal(cp); err != nil {
			t.Fatal(err)
		}
		first.State.Sources[src] = state
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	next, err := p.Scan(context.Background(), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Changed) != 1 || next.Changed[0].Ref.ID != "kept" || next.Changed[0].CWD != want {
		t.Errorf("upgraded scan changed = %+v, want kept with CWD %q", next.Changed, want)
	}
	if len(next.Removed) != 1 || next.Removed[0] != (model.SessionRef{Agent: model.AgentClaude, ID: "gone"}) {
		t.Errorf("upgraded scan removed = %+v, want gone", next.Removed)
	}
	var cp checkpoint
	if err := json.Unmarshal(next.State.Sources[path].Checkpoint, &cp); err != nil {
		t.Fatal(err)
	}
	if cp.Version != checkpointVersion || cp.Meta.CWD != want {
		t.Errorf("new checkpoint version, CWD = %d, %q", cp.Version, cp.Meta.CWD)
	}

	// The upgraded checkpoint makes the next unchanged scan a no-op.
	again, err := p.Scan(context.Background(), next.State)
	if err != nil || len(again.Changed) != 0 || len(again.Removed) != 0 {
		t.Errorf("scan after upgrade = %+v, %+v, %v", again.Changed, again.Removed, err)
	}
}
