package opencode

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// windowsPathCases are synthetic OpenCode-on-Windows session directories
// and project worktrees, with the CWD and repository root every host should
// report. None of them exist.
var windowsPathCases = []struct {
	name                string
	directory, worktree string
	wantCWD, wantRepo   string
}{
	{
		name:      "drive",
		directory: `c:/agent-sessions-synthetic/Proj/sub/`, worktree: `c:/agent-sessions-synthetic/Proj`,
		wantCWD: `C:\agent-sessions-synthetic\Proj\sub`, wantRepo: `C:\agent-sessions-synthetic\Proj`,
	},
	{
		name:      "mixed separators",
		directory: `C:\agent-sessions-synthetic/Proj\a\..\sub\.`, worktree: `C:\agent-sessions-synthetic\Proj\`,
		wantCWD: `C:\agent-sessions-synthetic\Proj\sub`, wantRepo: `C:\agent-sessions-synthetic\Proj`,
	},
	{
		name:      "long path prefix",
		directory: `\\?\D:\agent-sessions-synthetic\x\pkg\`, worktree: `\\?\D:\agent-sessions-synthetic\x`,
		wantCWD: `D:\agent-sessions-synthetic\x\pkg`, wantRepo: `D:\agent-sessions-synthetic\x`,
	},
	{
		name:      "unc",
		directory: `\\localhost\agent-sessions-missing\repo\pkg\`, worktree: `\\?\UNC\localhost\agent-sessions-missing\repo`,
		wantCWD: `\\localhost\agent-sessions-missing\repo\pkg`, wantRepo: `\\localhost\agent-sessions-missing\repo`,
	},
}

func TestWindowsPathsScanAndLoad(t *testing.T) {
	f := newScanFixture(t)
	for i, tc := range windowsPathCases {
		project := "proj-win-" + string(rune('a'+i))
		f.project(project, tc.worktree)
		f.session("v2-"+tc.name, map[string]any{"project_id": project, "directory": tc.directory})
		f.message("v2-"+tc.name, "user", 1, `{"text":"Fix the Windows build"}`)
		f.v1Session("v1-"+tc.name, map[string]any{"project_id": project, "directory": tc.directory})
	}

	byID := make(map[string]model.SessionMeta)
	for _, meta := range f.metas(t) {
		byID[meta.Ref.ID] = meta
	}
	for _, tc := range windowsPathCases {
		for _, id := range []string{"v2-" + tc.name, "v1-" + tc.name} {
			meta, ok := byID[id]
			if !ok {
				t.Errorf("%s: not scanned", id)
				continue
			}
			if meta.CWD != tc.wantCWD || meta.RepoRoot != tc.wantRepo {
				t.Errorf("%s: CWD, RepoRoot = %q, %q; want %q, %q", id, meta.CWD, meta.RepoRoot, tc.wantCWD, tc.wantRepo)
			}
			if !meta.CWDMissing {
				t.Errorf("%s: CWDMissing = false for %q", id, meta.CWD)
			}
		}
	}

	p := New(f.root, pathutil.NewGitResolver())
	for _, tc := range windowsPathCases {
		tx, err := p.Load(t.Context(), model.SessionRef{Agent: model.AgentOpenCode, ID: "v2-" + tc.name})
		if err != nil {
			t.Fatalf("load %s: %v", tc.name, err)
		}
		if tx.Meta.CWD != tc.wantCWD || tx.Meta.RepoRoot != tc.wantRepo || !tx.Meta.CWDMissing {
			t.Errorf("load %s: CWD, RepoRoot, missing = %q, %q, %v; want %q, %q, true",
				tc.name, tx.Meta.CWD, tx.Meta.RepoRoot, tx.Meta.CWDMissing, tc.wantCWD, tc.wantRepo)
		}
	}
}

// A relative or global worktree is not a repository root; the CWD is then
// resolved through Git, which never finds a repository for a foreign path.
func TestWindowsPathsWithoutUsableWorktree(t *testing.T) {
	f := newScanFixture(t)
	f.project("proj-relative", `agent-sessions-synthetic\Proj`)
	f.project("global", `C:\agent-sessions-synthetic`)
	f.session("relative", map[string]any{"project_id": "proj-relative", "directory": `C:\agent-sessions-synthetic\Proj\`})
	f.session("global", map[string]any{"project_id": "global", "directory": `\\?\C:\agent-sessions-synthetic\Proj`})

	metas := f.metas(t)
	if len(metas) != 2 {
		t.Fatalf("scanned %d sessions, want 2: %+v", len(metas), metas)
	}
	for _, meta := range metas {
		if meta.CWD != `C:\agent-sessions-synthetic\Proj` {
			t.Errorf("%s: CWD = %q", meta.Ref.ID, meta.CWD)
		}
		if runtime.GOOS != "windows" && meta.RepoRoot != "" {
			t.Errorf("%s: RepoRoot = %q, want none for a foreign path", meta.Ref.ID, meta.RepoRoot)
		}
	}
}

// Checkpoints of versions 2 and 3 hold metas normalized by the old rule,
// which left Windows paths raw on other hosts. Rows older than the cursor are
// never re-read incrementally, so the upgrade rescans in full, re-emits every
// session normalized, and still removes sessions deleted since.
func TestWindowsPathsOldCheckpointRescanned(t *testing.T) {
	const rawCWD, rawRepo = `c:/agent-sessions-synthetic/Proj/sub/`, `c:/agent-sessions-synthetic/Proj`
	const wantCWD, wantRepo = `C:\agent-sessions-synthetic\Proj\sub`, `C:\agent-sessions-synthetic\Proj`
	for _, version := range []int{2, 3} {
		t.Run("v"+strconv.Itoa(version), func(t *testing.T) {
			f := newScanFixture(t)
			f.project("proj-win", rawRepo)
			f.session("old", map[string]any{"project_id": "proj-win", "directory": rawCWD})
			f.session("new", map[string]any{"project_id": "proj-win", "directory": rawCWD, "time_updated": int64(1_700_000_900_000)})
			p := New(f.root, pathutil.NewGitResolver())
			first, err := p.Scan(t.Context(), provider.ScanState{})
			if err != nil || len(first.Changed) != 2 {
				t.Fatalf("first scan = %+v, %v", first.Changed, err)
			}

			// Rewrite the checkpoint as the old version wrote it, with a
			// session deleted from the database since.
			path := filepath.Join(f.root, dbName)
			state := first.State.Sources[path]
			var cp map[string]any
			if err := json.Unmarshal(state.Checkpoint, &cp); err != nil {
				t.Fatal(err)
			}
			cp["version"] = version
			metas := cp["metas"].(map[string]any)
			for _, id := range []string{"old", "new"} {
				metas[id].(map[string]any)["cwd"] = rawCWD
				metas[id].(map[string]any)["repoRoot"] = rawRepo
			}
			metas["gone"] = map[string]any{"ref": map[string]any{"agent": string(model.AgentOpenCode), "id": "gone"}, "cwd": rawCWD}
			cp["gen"].(map[string]any)["gone"] = GenV2
			cp["in"].(map[string]any)["gone"] = GenV2
			if state.Checkpoint, err = json.Marshal(cp); err != nil {
				t.Fatal(err)
			}
			first.State.Sources[path] = state

			next, err := p.Scan(t.Context(), first.State)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, meta := range next.Changed {
				ids = append(ids, meta.Ref.ID)
				if meta.CWD != wantCWD || meta.RepoRoot != wantRepo {
					t.Errorf("%s: CWD, RepoRoot = %q, %q; want %q, %q", meta.Ref.ID, meta.CWD, meta.RepoRoot, wantCWD, wantRepo)
				}
			}
			if !slices.Equal(ids, []string{"new", "old"}) {
				t.Errorf("upgraded scan changed %v, want [new old]", ids)
			}
			if len(next.Removed) != 1 || next.Removed[0] != (model.SessionRef{Agent: model.AgentOpenCode, ID: "gone"}) {
				t.Errorf("upgraded scan removed = %+v, want gone", next.Removed)
			}
			var after dbCheckpoint
			if err := json.Unmarshal(next.State.Sources[path].Checkpoint, &after); err != nil {
				t.Fatal(err)
			}
			if after.Version != dbCheckpointVersion || after.Metas["old"].CWD != wantCWD {
				t.Errorf("new checkpoint = version %d, old CWD %q", after.Version, after.Metas["old"].CWD)
			}

			// The upgraded checkpoint makes the next unchanged scan a no-op.
			again, err := p.Scan(t.Context(), next.State)
			if err != nil || len(again.Changed) != 0 || len(again.Removed) != 0 {
				t.Errorf("scan after upgrade = %+v, %+v, %v", again.Changed, again.Removed, err)
			}
		})
	}
}
