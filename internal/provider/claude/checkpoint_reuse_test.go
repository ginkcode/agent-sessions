package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/ginkcode/agent-sessions/internal/provider"
)

func TestCheckpointReuseIdentityAndReset(t *testing.T) {
	root, path := fixtureRoot(t, "split_assistant")
	p := New(root, nil)
	scan := func(p *Provider, prev provider.ScanState, decodes int64, changed int) provider.ScanResult {
		t.Helper()
		w := new(provider.ScanWork)
		r, err := p.Scan(provider.WithScanWork(context.Background(), w), prev)
		if err != nil || len(r.Changed) != changed || w.CheckpointDecodes.Load() != decodes {
			t.Fatalf("scan changed=%d decodes=%d err=%v", len(r.Changed), w.CheckpointDecodes.Load(), err)
		}
		return r
	}
	cold := scan(p, provider.ScanState{}, 0, 1)
	original, _ := json.Marshal(cold.State)
	scan(p, cold.State, 0, 0)
	fresh := New(root, nil)
	scan(fresh, cold.State, 1, 0)
	scan(fresh, cold.State, 0, 0)
	scan(p, provider.ScanState{}, 0, 1) // caller reset cannot reuse retained summaries
	for _, version := range []int{0, checkpointVersion - 1, checkpointVersion + 1} {
		old := cold.State.Sources[path]
		var cp checkpoint
		if err := json.Unmarshal(old.Checkpoint, &cp); err != nil {
			t.Fatal(err)
		}
		cp.Version = version
		old.Checkpoint, _ = json.Marshal(cp)
		scan(p, provider.ScanState{Sources: map[string]provider.SourceState{path: old}}, 1, 1)
	}
	// Corruption and in-place mutation must not match an aliased raw identity.
	old := cold.State.Sources[path]
	old.Checkpoint = append([]byte(nil), old.Checkpoint...)
	scan(p, provider.ScanState{Sources: map[string]provider.SourceState{path: old}}, 0, 0)
	old.Checkpoint[0] = '!'
	scan(p, provider.ScanState{Sources: map[string]provider.SourceState{path: old}}, 1, 1)
	// A valid caller-supplied checkpoint with different sidecar dependencies must
	// decode, not inherit an unchanged decision from another state.
	old = cold.State.Sources[path]
	var cp checkpoint
	_ = json.Unmarshal(old.Checkpoint, &cp)
	cp.SubagentMetaPath = "foreign"
	old.Checkpoint, _ = json.Marshal(cp)
	scan(p, provider.ScanState{Sources: map[string]provider.SourceState{path: old}}, 1, 1)
	after, _ := json.Marshal(cold.State)
	if string(after) != string(original) {
		t.Fatal("prior ScanState mutated")
	}
	// Atomic source replacement with identical stats must miss provider-owned
	// file identity, and rebuild rather than resume at EOF.
	scan(p, cold.State, 0, 0)
	info, _ := os.Stat(path)
	data, _ := os.ReadFile(path)
	replacement := path + ".replacement"
	if err := os.WriteFile(replacement, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	rebuilt := scan(p, cold.State, 0, 1)
	if !reflect.DeepEqual(rebuilt.Changed, cold.Changed) {
		t.Fatal("replacement metadata differs")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	removed := scan(p, rebuilt.State, 0, 0)
	if len(removed.Removed) != 1 || len(p.checkpointReuse.entries) != 0 {
		t.Fatal("removal retained summary")
	}
}

func TestCheckpointReuseSidecarRemoval(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "project", "parent", "subagents")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent-a1.jsonl")
	metaPath := filepath.Join(dir, "agent-a1.meta.json")
	if err := os.WriteFile(path, []byte("{\"type\":\"user\",\"message\":{\"content\":\"fallback\"}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metaPath, []byte(`{"description":"sidecar","agentType":"Explore"}`), 0600); err != nil {
		t.Fatal(err)
	}
	p := New(root, nil)
	cold, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(metaPath); err != nil {
		t.Fatal(err)
	}
	w := new(provider.ScanWork)
	r, err := p.Scan(provider.WithScanWork(context.Background(), w), cold.State)
	if err != nil || len(r.Changed) != 1 || r.Changed[0].Title != "fallback" || r.Changed[0].AgentName != "" || w.CheckpointDecodes.Load() != 1 {
		t.Fatalf("sidecar removal: %+v %v", r.Changed, err)
	}
}

func TestCheckpointReuseRewriteAndTruncate(t *testing.T) {
	root, path := fixtureRoot(t, "basic")
	p := New(root, nil)
	cold, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(cold.State)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	data = []byte(strings.ReplaceAll(string(data), "Fix the login bug", "Fix the other bug"))
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	stamp := info.ModTime().Add(time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	rewritten, err := p.Scan(context.Background(), cold.State)
	if err != nil || len(rewritten.Changed) != 1 || rewritten.Changed[0].FirstPrompt != "Fix the other bug" {
		t.Fatalf("rewrite: %+v %v", rewritten.Changed, err)
	}
	if err := os.WriteFile(path, []byte("{\"type\":\"user\",\"message\":{\"content\":\"short\"}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	truncated, err := p.Scan(context.Background(), rewritten.State)
	if err != nil || len(truncated.Changed) != 1 || truncated.Changed[0].Counts.User != 1 || truncated.Changed[0].FirstPrompt != "short" {
		t.Fatalf("truncate: %+v %v", truncated.Changed, err)
	}
	after, _ := json.Marshal(cold.State)
	if string(before) != string(after) {
		t.Fatal("prior checkpoint changed")
	}
}

func TestCheckpointReuseConcurrentScans(t *testing.T) {
	root, _ := fixtureRoot(t, "split_assistant")
	p := New(root, nil)
	cold, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := p.Scan(context.Background(), cold.State)
			if err != nil || len(r.Changed) != 0 {
				t.Errorf("concurrent scan: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestCheckpointReuseBounds(t *testing.T) {
	root, path := fixtureRoot(t, "basic")
	p := New(root, nil)
	r, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	state := r.State.Sources[path]
	info, _ := os.Stat(path)
	cp := checkpoint{Version: checkpointVersion}
	for i := 0; i < checkpointReuseLimit+5; i++ {
		p.checkpointReuse.remember(source{Path: strings.Repeat("x", i+1)}, state, info, &cp)
	}
	if len(p.checkpointReuse.entries) != checkpointReuseLimit {
		t.Fatal("entry bound exceeded")
	}
	state.Checkpoint = make([]byte, checkpointReuseMaxBytes+1)
	p.checkpointReuse.remember(source{Path: "oversized"}, state, info, &cp)
	if _, ok := p.checkpointReuse.entries["oversized"]; ok {
		t.Fatal("oversized checkpoint admitted")
	}
	p.checkpointReuse.prune(nil, provider.ScanState{})
	if len(p.checkpointReuse.entries) != 0 {
		t.Fatal("reset did not prune")
	}
}

func TestCheckpointReuseRetainedHeap(t *testing.T) {
	_, path := fixtureRoot(t, "basic")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, checkpointReuseLimit)
	for i := range paths {
		paths[i] = path + strings.Repeat("x", i%8) + string(rune(i+1))
	}
	state := provider.SourceState{Checkpoint: []byte(strings.Repeat("x", 802))}
	var cache checkpointReuse
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for _, path := range paths {
		cache.remember(source{Path: path}, state, info, &checkpoint{})
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(&cache)
	runtime.KeepAlive(paths)
	t.Logf("retained heap delta=%d bytes for %d summaries (802-byte synthetic raw identity); includes map/raw allocations, excludes preallocated paths/shared FileInfo", int64(after.HeapAlloc)-int64(before.HeapAlloc), checkpointReuseLimit)
}

func TestCheckpointReuseRetainedCost(t *testing.T) {
	root, path := fixtureRoot(t, "basic")
	p := New(root, nil)
	r, err := p.Scan(context.Background(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	s := p.checkpointReuse.entries[path]
	// Explicit payload accounting, not a heap measurement: excludes allocator,
	// map buckets and platform-specific os.FileInfo backing storage.
	t.Logf("summary struct=%d raw=%d path=%d metaPath=%d checkpoint=%d accounted=%d bytes", unsafe.Sizeof(s), len(s.raw), len(path), len(s.metaPath), len(r.State.Sources[path].Checkpoint), int(unsafe.Sizeof(s))+len(s.raw)+len(path)+len(s.metaPath))
}
