package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cd "github.com/ginkcode/agent-sessions/internal/codediagnostics"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func TestCodeDiagnosticsKnownNonGitWorkspaceNestedProjects(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	dir := filepath.Join(parent, "workspace")
	for name, body := range map[string]string{
		"api/pyproject.toml":        "[project]\nname = \"api\"\nversion = \"0.1.0\"\n",
		"api/main.py":               "def greeting() -> str:\n    return 'hello'\n",
		"web/package.json":          "{\"name\":\"web\",\"private\":true}\n",
		"web/tsconfig.json":         "{\"compilerOptions\":{\"strict\":true},\"include\":[\"*.ts\"]}\n",
		"web/main.ts":               "export const greeting: string = 'hello';\n",
		"processing/pyproject.toml": "[project]\nname = \"processing\"\nversion = \"0.1.0\"\n",
		"processing/main.py":        "def process(value: int) -> int:\n    return value + 1\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace must have no .git: %v", err)
	}

	// Only the workspace CWD is catalog authority: there is deliberately no
	// RepoRoot and no catalog entry for any nested project or ancestor.
	catalog := scan.NewCatalog()
	catalog.Reset([]model.SessionMeta{{
		Ref: model.SessionRef{Agent: model.AgentClaude, ID: "non-git-workspace"},
		CWD: dir,
	}})

	// Exercise real adapter discovery, but only with synthetic reviewed paths.
	// No installed host tools, server launch, approval, or network are needed.
	home, tools := t.TempDir(), t.TempDir()
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var discoveries, launches atomic.Int32
	discover := func(root cd.AuthorizedRoot) ([]cd.Registration, error) {
		discoveries.Add(1)
		<-release
		return cd.DiscoverLocalLive(root, cd.LocalLiveEnvironment{
			GoPath: []string{tools}, Path: []string{tools},
			Go:      cd.GoplsEnvironment{Home: home, Cache: home, ModuleCache: home},
			TS:      cd.TSEnvironment{Home: home},
			Ruff:    cd.RuffEnvironment{Home: home},
			Pyright: cd.PyrightEnvironment{Home: home},
		})
	}
	owner, err := newDiagnosticsOwnerWithLive(t.TempDir(), "workspace-test-host", discover, cd.SessionOptions{
		Launch: func(cd.Invocation) (cd.SessionProcess, cd.SessionStreams, error) {
			launches.Add(1)
			return nil, cd.SessionStreams{}, errors.New("workspace discovery must not launch a server")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(WithService(NewService(catalog, nil)), WithCacheEnabled(false))
	e.codeDiagnostics = owner
	go owner.events(e)
	t.Cleanup(func() {
		unblock()
		_ = e.Close()
	})

	// An ancestor of a known CWD is still arbitrary, not implicitly authorized.
	_, err = e.AttachCodeDiagnostics(ctx, parent, 1)
	requireDiagnosticCode(t, err, "unauthorized")
	if discoveries.Load() != 0 {
		t.Fatal("unknown parent reached adapter discovery")
	}

	// Discovery is held behind release, so returning here also proves attach is
	// asynchronous and accepts a catalog-known non-Git workspace as its root.
	attached, err := e.AttachCodeDiagnostics(ctx, dir, 1)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if attached.Project.Root != canonical {
		t.Fatalf("attachment root = %q, want workspace %q", attached.Project.Root, canonical)
	}
	unblock()

	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, err := e.GetCodeDiagnosticsSnapshot(ctx, attached.Context)
		if err != nil {
			t.Fatal(err)
		}
		states := make(map[string]string)
		for _, provider := range snapshot.Providers {
			states[provider.ProviderID] = provider.Availability
		}
		complete := true
		for _, id := range []string{"ruff", "pyright", "typescript"} {
			state, present := states[id]
			if !present {
				complete = false
				continue
			}
			if state != "missingTool" && state != "available" {
				t.Fatalf("nested workspace provider %s = %q, want missingTool or available; providers = %+v", id, state, snapshot.Providers)
			}
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("async workspace discovery did not expose Python and TypeScript providers: %+v", snapshot.Providers)
		}
		time.Sleep(time.Millisecond)
	}
	if discoveries.Load() != 1 || launches.Load() != 0 {
		t.Fatalf("discoveries = %d, launches = %d; want one metadata discovery and no execution", discoveries.Load(), launches.Load())
	}
	_, err = e.AttachCodeDiagnostics(ctx, parent, 1)
	requireDiagnosticCode(t, err, "unauthorized")
}
