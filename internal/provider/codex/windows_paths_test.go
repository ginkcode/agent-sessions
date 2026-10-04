package codex

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

// windowsCWDCases are synthetic Windows working directories as Codex on
// Windows records them, with the CWD every host should report. None of them
// exist; the drive root is the only one that may on a Windows host.
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

// windowsLine returns one rollout record with payload quoted by json.Marshal.
func windowsLine(t *testing.T, ts, kind string, payload map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"timestamp": ts, "type": kind, "payload": payload})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw) + "\n"
}

func windowsRollout(t *testing.T, id, metaCWD, turnCWD string) string {
	t.Helper()
	meta := map[string]any{"id": id, "timestamp": "2026-09-28T12:00:00Z", "cli_version": "0.156.1"}
	if metaCWD != "" {
		meta["cwd"] = metaCWD
	}
	return strings.Join([]string{
		windowsLine(t, "2026-09-28T12:00:00Z", "session_meta", meta),
		windowsLine(t, "2026-09-28T12:00:01Z", "turn_context", map[string]any{"cwd": turnCWD, "model": "gpt-5-codex"}),
		windowsLine(t, "2026-09-28T12:00:02Z", "response_item", map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "Fix the Windows build"}},
		}),
		windowsLine(t, "2026-09-28T12:00:03Z", "response_item", map[string]any{
			"type": "message", "role": "assistant", "id": "msg_1",
			"content": []any{map[string]any{"type": "output_text", "text": "Done."}},
		}),
	}, "")
}

func TestWindowsCWDScanAndLoad(t *testing.T) {
	t.Parallel()
	for _, tc := range windowsCWDCases {
		for _, from := range []string{"session_meta", "turn_context"} {
			t.Run(tc.name+"/"+from, func(t *testing.T) {
				t.Parallel()
				const id = "33333333-3333-4333-8333-333333333333"
				metaCWD, turnCWD := tc.raw, `E:\ignored\turn`
				if from == "turn_context" {
					metaCWD, turnCWD = "", tc.raw
				}
				root := platform.TempDir(t)
				path := writeRollout(t, root, id, windowsRollout(t, id, metaCWD, turnCWD), false)
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
				if (!tc.mayExist || runtime.GOOS != "windows") && !meta.CWDMissing {
					t.Errorf("scan CWDMissing = false for %q", meta.CWD)
				}
				if runtime.GOOS != "windows" && meta.RepoRoot != "" {
					t.Errorf("scan RepoRoot = %q for a foreign path", meta.RepoRoot)
				}

				tx, err := p.Load(context.Background(), model.SessionRef{Agent: model.AgentCodex, ID: id})
				if err != nil {
					t.Fatal(err)
				}
				if tx.Meta.CWD != tc.want || tx.Meta.CWDMissing != meta.CWDMissing {
					t.Errorf("load CWD = %q (missing %v), want %q (missing %v)", tx.Meta.CWD, tx.Meta.CWDMissing, tc.want, meta.CWDMissing)
				}

				// Appending resumes from the checkpoint, which already holds
				// the normalized CWD.
				f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				_, werr := f.WriteString(windowsLine(t, "2026-09-28T12:00:04Z", "response_item", map[string]any{
					"type": "message", "role": "user",
					"content": []any{map[string]any{"type": "input_text", "text": "And the tests"}},
				}))
				if cerr := f.Close(); werr == nil {
					werr = cerr
				}
				if werr != nil {
					t.Fatal(werr)
				}
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
}

// Checkpoints written before checkpointVersion 3 hold CWDs normalized by the
// old rule, which left Windows paths raw on other hosts. The upgrade rescans
// even unchanged rollouts so the catalog gets the normalized CWD, and still
// reports rollouts deleted since.
func TestWindowsCWDOldCheckpointRescanned(t *testing.T) {
	t.Parallel()
	const raw, want = `c:/agent-sessions-synthetic/Proj/`, `C:\agent-sessions-synthetic\Proj`
	const kept, gone = "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"
	root := platform.TempDir(t)
	keptPath := writeRollout(t, root, kept, windowsRollout(t, kept, raw, raw), false)
	gonePath := writeRollout(t, root, gone, windowsRollout(t, gone, raw, raw), false)
	p := New(root, nil)
	first, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil || len(first.Changed) != 2 {
		t.Fatalf("first scan = %+v, %v", first.Changed, err)
	}

	// Rewrite both checkpoints as version 2 wrote them.
	for src, state := range first.State.Sources {
		var cp map[string]any
		if err := json.Unmarshal(state.Checkpoint, &cp); err != nil {
			t.Fatal(err)
		}
		cp["version"] = 2
		cp["meta"].(map[string]any)["cwd"] = raw
		cp["canonical"].(map[string]any)["cwd"] = raw
		if state.Checkpoint, err = json.Marshal(cp); err != nil {
			t.Fatal(err)
		}
		first.State.Sources[src] = state
	}
	if err := os.Remove(gonePath); err != nil {
		t.Fatal(err)
	}

	next, err := p.Scan(context.Background(), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Changed) != 1 || next.Changed[0].Ref.ID != kept || next.Changed[0].CWD != want {
		t.Errorf("upgraded scan changed = %+v, want %s with CWD %q", next.Changed, kept, want)
	}
	if len(next.Removed) != 1 || next.Removed[0] != (model.SessionRef{Agent: model.AgentCodex, ID: gone}) {
		t.Errorf("upgraded scan removed = %+v, want %s", next.Removed, gone)
	}
	var cp checkpoint
	if err := json.Unmarshal(next.State.Sources[keptPath].Checkpoint, &cp); err != nil {
		t.Fatal(err)
	}
	if cp.Version != checkpointVersion || cp.Meta.CWD != want || cp.Canonical == nil || cp.Canonical.CWD != want {
		t.Errorf("new checkpoint = version %d, CWD %q, canonical %+v", cp.Version, cp.Meta.CWD, cp.Canonical)
	}

	// The upgraded checkpoint makes the next unchanged scan a no-op.
	again, err := p.Scan(context.Background(), next.State)
	if err != nil || len(again.Changed) != 0 || len(again.Removed) != 0 {
		t.Errorf("scan after upgrade = %+v, %+v, %v", again.Changed, again.Removed, err)
	}
}
