package manage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
)

type fakeImageProc struct {
	images map[int]string
	err    error
}

func (fakeImageProc) Cmdlines() (map[int][]string, error) {
	return nil, errors.New("command lines must not be read")
}

func (p fakeImageProc) Images() (map[int]string, error) { return p.images, p.err }

func idleImages() map[int]string { return map[int]string{1: "explorer.exe"} }

func writeGuardFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClassifyImages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		images map[int]string
		want   map[string]string
		err    error
	}{
		{name: "empty", err: ErrProcessUnknown},
		{name: "missing image", images: map[int]string{1: ""}, err: ErrProcessUnknown},
		{name: "idle", images: idleImages(), want: map[string]string{}},
		{
			name: "native agents and aliases",
			images: map[int]string{
				1: `C:\Program Files\Claude\CLAUDE.EXE`, 2: "codex-x86_64-pc-windows-msvc.exe",
				3: `D:/Apps/OPENCODE.exe`, 4: "claude-code.exe",
			},
			want: map[string]string{"claude-code": "claude-code.exe", "codex": "codex-x86_64-pc-windows-msvc.exe", "opencode": "opencode.exe"},
		},
		{
			// Without arguments a desktop app's server or helper service
			// cannot be told apart from a session, so both block deletion.
			name:   "helpers and servers block",
			images: map[int]string{1: "codex-windows-sandbox-service.exe", 2: "opencode-cli.exe"},
			want:   map[string]string{"codex": "codex-windows-sandbox-service.exe", "opencode": "opencode-cli.exe"},
		},
		{name: "unrelated", images: map[int]string{1: "claudette.exe", 2: "decodex.exe"}, want: map[string]string{}},
		{name: "node", images: map[int]string{1: "Node.exe"}, err: errRuntimeHost},
		{name: "nodejs", images: map[int]string{1: "nodejs"}, err: errRuntimeHost},
		{name: "bun", images: map[int]string{1: `C:\bin\bun.exe`}, err: errRuntimeHost},
		{name: "deno", images: map[int]string{1: "deno.EXE"}, err: errRuntimeHost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := classifyImages(tc.images)
			if !errors.Is(err, tc.err) || tc.err == nil && !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("classifyImages = %v, %v; want %v, %v", got, err, tc.want, tc.err)
			}
			if tc.err != nil && !errors.Is(err, ErrProcessUnknown) {
				t.Errorf("unknown process state must block: %v", err)
			}
		})
	}
}

func TestProcessSafetyErrorNamesExecutable(t *testing.T) {
	err := processSafetyError(map[string]string{"opencode": "opencode-cli.exe"}, nil, "opencode")
	if !errors.Is(err, ErrLive) || err.Error() != "session is live: agent process is running: opencode-cli.exe" {
		t.Fatalf("got %v", err)
	}
	if err := processSafetyError(map[string]string{"opencode": "opencode-cli.exe"}, nil, "codex"); err != nil {
		t.Fatalf("other agent blocked: %v", err)
	}
}

func TestImageProcGuardDoesNotReadCommandLines(t *testing.T) {
	g := newLiveGuard(fakeImageProc{images: map[int]string{1: "codex.exe"}})
	live, err := g.procLive(context.Background())
	if err != nil || live["codex"] != "codex.exe" {
		t.Fatalf("image guard = %v, %v", live, err)
	}
	for _, proc := range []fakeImageProc{{err: errors.New("private process path")}, {}} {
		_, err := newLiveGuard(proc).procLive(context.Background())
		if !errors.Is(err, ErrProcessUnknown) || strings.Contains(err.Error(), "private") {
			t.Fatalf("must fail closed without exposing process errors: %v", err)
		}
	}
}

func TestPreviewImageProcessGuard(t *testing.T) {
	for _, tc := range []struct {
		name      string
		proc      fakeImageProc
		permanent bool
		blocked   string
	}{
		{name: "idle Claude does not need permanent opt in", proc: fakeImageProc{images: idleImages()}},
		{name: "running Claude", proc: fakeImageProc{images: map[int]string{1: "claude.exe"}}, blocked: ErrLive.Error()},
		{name: "runtime host", proc: fakeImageProc{images: map[int]string{1: "node.exe"}}, blocked: errRuntimeHost.Error()},
		{name: "inspection failed", proc: fakeImageProc{err: errors.New("private")}, blocked: ErrProcessUnknown.Error()},
		{name: "empty snapshot", blocked: ErrProcessUnknown.Error()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roots := testRoots(t)
			meta := claudeMainMeta(roots.Claude, uuidA)
			writeGuardFixture(t, meta.SourcePath)
			backdate(t, roots.Claude)
			mgr, trash := newTestManager(t, roots, WithProcFS(tc.proc))
			if err := mgr.SetConfig(Config{Enabled: true, AllowPermanentDelete: tc.permanent}); err != nil {
				t.Fatal(err)
			}
			p, err := mgr.Preview(context.Background(), []model.SessionRef{meta.Ref}, []model.SessionMeta{meta})
			if err != nil || len(p.Items) != 1 {
				t.Fatalf("preview = %+v, %v", p, err)
			}
			if tc.blocked == "" && p.Items[0].Blocked != "" || tc.blocked != "" && !strings.Contains(p.Items[0].Blocked, tc.blocked) {
				t.Errorf("blocked = %q, want %q", p.Items[0].Blocked, tc.blocked)
			}
			if len(trash.calls) != 0 {
				t.Fatal("preview must not trash anything")
			}
		})
	}
}

func TestImageProcPermanentGuard(t *testing.T) {
	roots := testRoots(t)
	metas := []model.SessionMeta{codexMeta(roots.Codex, uuidA), opencodeMeta(roots.OpenCodeData, "ses_one")}
	for _, meta := range metas {
		for _, tc := range []struct {
			proc      fakeImageProc
			permanent bool
			want      error
		}{
			{proc: fakeImageProc{images: idleImages()}, want: ErrPermanentNotAllowed},
			{proc: fakeImageProc{images: map[int]string{1: "bun.exe"}}, permanent: true, want: errRuntimeHost},
			{proc: fakeImageProc{err: errors.New("failed")}, permanent: true, want: ErrProcessUnknown},
			{proc: fakeImageProc{images: map[int]string{1: "codex.exe", 2: "opencode.exe"}}, permanent: true, want: ErrLive},
		} {
			mgr, _ := newTestManager(t, roots, WithProcFS(tc.proc))
			live, procErr := mgr.procLiveMap(context.Background())
			_, err := mgr.planOne(context.Background(), meta, newCatalogIndex(metas), Config{Enabled: true, AllowPermanentDelete: tc.permanent}, live, procErr)
			if !errors.Is(err, tc.want) {
				t.Errorf("%s: got %v, want %v", meta.Ref.Agent, err, tc.want)
			}
		}
	}
}

type failingCmdlineProc struct{}

func (failingCmdlineProc) Cmdlines() (map[int][]string, error) {
	return nil, errors.New("private /proc failure")
}

// The /proc scan gates only permanent actions; Claude keeps its provider
// LiveFunc outside Windows, so a scan failure must not newly block it.
func TestCmdlineScanFailureGatesOnlyPermanent(t *testing.T) {
	roots := testRoots(t)
	claude := claudeMainMeta(roots.Claude, uuidA)
	writeGuardFixture(t, claude.SourcePath)
	backdate(t, roots.Claude)
	metas := []model.SessionMeta{claude, codexMeta(roots.Codex, uuidB), opencodeMeta(roots.OpenCodeData, "ses_one")}
	mgr, _ := newTestManager(t, roots, WithProcFS(failingCmdlineProc{}))
	live, procErr := mgr.procLiveMap(context.Background())
	if procErr == nil {
		t.Fatal("scan failure not reported")
	}
	cfg := Config{Enabled: true, AllowPermanentDelete: true}
	for _, meta := range metas {
		_, err := mgr.planOne(context.Background(), meta, newCatalogIndex(metas), cfg, live, procErr)
		if meta.Ref.Agent == model.AgentClaude {
			if errors.Is(err, ErrProcessUnknown) {
				t.Errorf("Claude gated by /proc scan: %v", err)
			}
			continue
		}
		if !errors.Is(err, ErrProcessUnknown) || strings.Contains(err.Error(), "private") {
			t.Errorf("%s: got %v, want sanitized %v", meta.Ref.Agent, err, ErrProcessUnknown)
		}
	}
}

// changingImageProc supplies a fresh table on each scan, simulating an agent
// starting between preview and execution or between two recycled paths.
type changingImageProc struct {
	calls  int
	block  int
	images map[int]string
}

func (*changingImageProc) Cmdlines() (map[int][]string, error) { return nil, ErrProcessUnknown }
func (p *changingImageProc) Images() (map[int]string, error) {
	p.calls++
	if p.calls >= p.block {
		return p.images, nil
	}
	return idleImages(), nil
}

func TestDeleteRechecksImageProcesses(t *testing.T) {
	for _, block := range []int{2, 4, 5} {
		t.Run(strconv.Itoa(block), func(t *testing.T) {
			roots := testRoots(t)
			meta := claudeMainMeta(roots.Claude, uuidA)
			writeGuardFixture(t, meta.SourcePath)
			writeGuardFixture(t, filepath.Join(filepath.Dir(meta.SourcePath), uuidA, "artifact.txt"))
			backdate(t, roots.Claude)
			proc := &changingImageProc{block: block, images: map[int]string{1: "claude.exe"}}
			mgr, trash := newTestManager(t, roots, WithProcFS(proc))
			enableAll(t, mgr)
			catalog := []model.SessionMeta{meta}
			p, err := mgr.Preview(context.Background(), []model.SessionRef{meta.Ref}, catalog)
			if err != nil || p.Items[0].Blocked != "" {
				t.Fatalf("preview = %+v, %v", p, err)
			}
			r, err := mgr.Delete(context.Background(), []model.SessionRef{meta.Ref}, catalog, p.Token)
			if block == 2 {
				if !errors.Is(err, ErrPreviewStale) || len(trash.calls) != 0 {
					t.Fatalf("changed guard must stale token before trash: %+v, %v, %v", r, err, trash.calls)
				}
				return
			}
			if err != nil || r.Failed != 1 || r.Deleted != 0 || len(r.Forgotten) != 0 {
				t.Fatalf("guard failure must not forget the session: %+v, %v", r, err)
			}
			wantMoved := block - 4
			if len(trash.calls) != wantMoved || len(r.Items[0].Moved) != wantMoved || len(r.Items[0].Remaining) != 2-wantMoved {
				t.Fatalf("per-file guard must preserve partial result: %+v, calls=%v", r, trash.calls)
			}
			if !strings.Contains(r.Items[0].Error, ErrLive.Error()) {
				t.Fatalf("wrong guard error: %+v", r.Items[0])
			}
		})
	}
}

type checkedFakeTrash struct {
	fakeTrash
	blocked error
}

func (*checkedFakeTrash) DisplayName() string           { return "Recycle Bin" }
func (f *checkedFakeTrash) CheckTrashPath(string) error { return f.blocked }

func TestDeleteTrashOutcomeUnknown(t *testing.T) {
	for _, failAt := range []int{0, 1} {
		t.Run(strconv.Itoa(failAt), func(t *testing.T) {
			roots := testRoots(t)
			meta := claudeMainMeta(roots.Claude, uuidA)
			sessionDir := filepath.Join(filepath.Dir(meta.SourcePath), uuidA)
			files := []string{meta.SourcePath, sessionDir}
			writeGuardFixture(t, meta.SourcePath)
			writeGuardFixture(t, filepath.Join(sessionDir, "artifact.txt"))
			backdate(t, roots.Claude)
			trash := &fakeTrash{fail: map[string]error{files[failAt]: ErrTrashOutcomeUnknown}}
			mgr, _ := newTestManager(t, roots, WithTrash(trash))
			enableAll(t, mgr)
			catalog := []model.SessionMeta{meta}
			p, err := mgr.Preview(context.Background(), []model.SessionRef{meta.Ref}, catalog)
			if err != nil {
				t.Fatal(err)
			}
			r, err := mgr.Delete(context.Background(), []model.SessionRef{meta.Ref}, catalog, p.Token)
			if err != nil || r.Failed != 1 || r.Deleted != 0 || len(r.Forgotten) != 0 {
				t.Fatalf("unknown outcome must not forget the session: %+v, %v", r, err)
			}
			item := r.Items[0]
			if !reflect.DeepEqual(item.Unknown, []string{files[failAt]}) || len(item.Moved) != failAt || len(item.Remaining) != 1-failAt {
				t.Fatalf("unknown and remaining must be distinct: %+v", item)
			}
			if failAt == 0 && item.Remaining[0] != sessionDir || failAt == 1 && item.Moved[0] != meta.SourcePath {
				t.Fatalf("partial progress attributed to wrong path: %+v", item)
			}
			if item.Error != ErrTrashOutcomeUnknown.Error() {
				t.Fatalf("outcome warning lost: %+v", item)
			}
		})
	}
}

func TestPreviewTrashDestinationAndValidation(t *testing.T) {
	roots := testRoots(t)
	meta := claudeMainMeta(roots.Claude, uuidA)
	writeGuardFixture(t, meta.SourcePath)
	backdate(t, roots.Claude)
	trash := &checkedFakeTrash{blocked: errors.New("this location has no Recycle Bin")}
	mgr, _ := newTestManager(t, roots, WithTrash(trash))
	enableAll(t, mgr)
	p, err := mgr.Preview(context.Background(), []model.SessionRef{meta.Ref}, []model.SessionMeta{meta})
	if err != nil || p.TrashLabel != "Recycle Bin" || p.Items[0].Blocked != trash.blocked.Error() {
		t.Fatalf("trash preview = %+v, %v", p, err)
	}
	if len(trash.calls) != 0 {
		t.Fatal("blocked preview invoked trash")
	}
	if _, err := os.Stat(meta.SourcePath); err != nil {
		t.Fatal(err)
	}
}
