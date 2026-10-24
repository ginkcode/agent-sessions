package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

func TestSourceAcceptsWithoutUnrelatedParsing(t *testing.T) {
	p, ref, _ := syntheticRollout(t,
		record("session_meta", `{"id":"fixture"}`),
		record("response_item", `{"type":"message","role":"user","content":[{"type":"input_file","file_data":"data:text/plain;base64,SEVMTE8="}]}`),
	)
	indexRollout(t, p.root, indexID, false)
	ctx := context.Background()
	want, err := p.Load(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	hint := want.Meta
	if _, err := p.LoadFromSource(ctx, ref, hint); err != nil {
		t.Fatal(err)
	}
	snapshot := p.sourceSnapshot
	key := want.Messages[0].Parts[0].File.Ref
	var work provider.ScanWork
	var proof SourceWork
	observed := WithSourceWork(provider.WithScanWork(ctx, &work), &proof)
	got, err := p.LoadFromSource(observed, ref, hint)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("load: %v, %v", got, err)
	}
	data, err := p.BlobFromSource(observed, ref, hint, key)
	if err != nil || string(data) != "HELLO" {
		t.Fatalf("blob: %q, %v", data, err)
	}
	if work.SourceOpens.Load() != 0 || work.Records.Load() != 0 || work.CheckpointDecodes.Load() != 0 || work.CheckpointEncodes.Load() != 0 {
		t.Fatalf("accepting path parsed unrelated sources: %+v", &work)
	}
	if proof.Passes.Load() != 2 || proof.Bytes.Load() == 0 {
		t.Fatalf("proof not accounted: passes=%d bytes=%d", proof.Passes.Load(), proof.Bytes.Load())
	}
	// Baseline resolution parses both sources, proving these counters are live.
	if _, err := p.Load(observed, ref); err != nil {
		t.Fatal(err)
	}
	if proof.Passes.Load() != 2 {
		t.Fatal("ordinary load performed proof")
	}
	if work.SourceOpens.Load() != 2 || work.Records.Load() == 0 {
		t.Fatalf("baseline opens=%d records=%d", work.SourceOpens.Load(), work.Records.Load())
	}
	if snapshot != p.sourceSnapshot {
		t.Fatal("ordinary scan replaced optional snapshot")
	}
	if snapshot.canonical[ref] != hint {
		t.Fatal("previous snapshot mutated")
	}
	forged := hint
	forged.Title = "forged"
	if _, err := p.LoadFromSource(ctx, ref, forged); !errors.Is(err, provider.ErrStaleSource) {
		t.Fatalf("forged hint: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.LoadFromSource(canceled, ref, hint); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	fresh := New(p.root, nil)
	if tr, err := fresh.LoadFromSource(ctx, ref, hint); err != nil || !reflect.DeepEqual(tr, want) {
		t.Fatalf("unproven new provider: %v", err)
	}
	if tr, err := provider.LoadWithSource(ctx, fresh, ref, hint); err != nil || !reflect.DeepEqual(tr, want) {
		t.Fatalf("standalone fallback: %v", err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.LoadFromSource(ctx, ref, hint); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestSourceStaleReconciliation(t *testing.T) {
	for _, mutation := range []string{"same-stat rewrite", "duplicate", "index", "disappear", "replace", "symlink"} {
		t.Run(mutation, func(t *testing.T) {
			p, ref, path := syntheticRollout(t, record("session_meta", `{"id":"fixture"}`), record("response_item", `{"type":"message","role":"user","content":"before"}`))
			ctx := context.Background()
			initial, err := p.Load(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			hint := initial.Meta
			if _, err := p.LoadFromSource(ctx, ref, hint); err != nil {
				t.Fatal(err)
			}
			before := mustRead(t, path)
			switch mutation {
			case "same-stat rewrite":
				stat, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				changed := strings.Replace(string(before), "before", "after!", 1)
				if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, stat.ModTime(), stat.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				duplicate := filepath.Join(p.root, "archived_sessions", "rollout-new.jsonl")
				if err := os.MkdirAll(filepath.Dir(duplicate), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(duplicate, append(before, []byte(line("2099-01-01T00:00:00Z", "event_msg", `{"type":"task_complete"}`))...), 0o600); err != nil {
					t.Fatal(err)
				}
			case "index":
				db := stateDB(t, p.root, "state_99.sqlite", stateSQL)
				insertThread(t, db, ref.ID, path, "changed title", 1)
			case "disappear":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "replace":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, before, 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				outside := filepath.Join(t.TempDir(), "rollout-outside.jsonl")
				if err := os.WriteFile(outside, before, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Skip(err)
				}
			}
			blobProvider := New(p.root, nil)
			blobProvider.sourceSnapshot = p.sourceSnapshot
			if _, err := p.LoadFromSource(ctx, ref, hint); !errors.Is(err, provider.ErrStaleSource) {
				t.Fatalf("load accepted stale evidence: %v", err)
			}
			if _, err := blobProvider.BlobFromSource(ctx, ref, hint, "rec:0:0"); !errors.Is(err, provider.ErrStaleSource) {
				t.Fatalf("blob accepted stale evidence: %v", err)
			}
			want, wantErr := New(p.root, nil).Load(ctx, ref)
			got, gotErr := provider.LoadWithSource(ctx, p, ref, hint)
			if (wantErr == nil) != (gotErr == nil) || !reflect.DeepEqual(got, want) {
				t.Fatalf("fallback mismatch: %v / %v", gotErr, wantErr)
			}
		})
	}
}

func TestSourceIndexOverlayChange(t *testing.T) {
	root := t.TempDir()
	path := indexRollout(t, root, indexID, false)
	db := stateDB(t, root, "state_1.sqlite", stateSQL)
	insertThread(t, db, indexID, path, "original", 0)
	p := New(root, nil)
	ref := model.SessionRef{Agent: model.AgentCodex, ID: indexID}
	ctx := context.Background()
	tr, err := p.Load(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadFromSource(ctx, ref, tr.Meta); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE threads SET title='changed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadFromSource(ctx, ref, tr.Meta); !errors.Is(err, provider.ErrStaleSource) {
		t.Fatal(err)
	}
	got, err := provider.LoadWithSource(ctx, p, ref, tr.Meta)
	if err != nil || got.Meta.Title != "changed" {
		t.Fatalf("overlay not reconciled: %v, %v", got, err)
	}
}

func TestSourceProofBound(t *testing.T) {
	p, _, path := syntheticRollout(t, record("session_meta", `{"id":"fixture"}`))
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxProofBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()
	if p.proveSources(context.Background()) != nil {
		t.Fatal("oversized source retained")
	}
}

// OrdinaryScanBefore is the unchanged implementation, identical to pre-PF07.
// OrdinaryScanAfter seeds an optional snapshot first, checking no-hint isolation.
func BenchmarkSourceReconciliation(b *testing.B) {
	root := b.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "09", "28")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		id := fmt.Sprintf("fixture-%d", i)
		content := record("session_meta", `{"id":"`+id+`"}`) + strings.Repeat(record("response_item", `{"type":"message","role":"user","content":"synthetic prompt"}`), 100)
		if err := os.WriteFile(filepath.Join(dir, "rollout-"+id+".jsonl"), []byte(content), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	ref := model.SessionRef{Agent: model.AgentCodex, ID: "fixture-0"}
	p := New(root, nil)
	initial, err := p.Load(context.Background(), ref)
	if err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"OrdinaryScanBefore", "OrdinaryScanAfter", "OrdinaryLoad", "OptionalCold", "OptionalWarm"} {
		b.Run(mode, func(b *testing.B) {
			p := New(root, nil)
			if mode == "OptionalWarm" || mode == "OrdinaryScanAfter" {
				if _, err := p.LoadFromSource(context.Background(), ref, initial.Meta); err != nil {
					b.Fatal(err)
				}
			}
			var work SourceWork
			ctx := WithSourceWork(context.Background(), &work)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				var err error
				switch mode {
				case "OrdinaryScanBefore", "OrdinaryScanAfter":
					_, err = p.Scan(ctx, provider.ScanState{})
				case "OrdinaryLoad":
					_, err = p.Load(ctx, ref)
				case "OptionalCold":
					_, err = New(root, nil).LoadFromSource(ctx, ref, initial.Meta)
				case "OptionalWarm":
					_, err = p.LoadFromSource(ctx, ref, initial.Meta)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(work.Bytes.Load())/float64(b.N), "proof-B/op")
			b.ReportMetric(float64(work.Passes.Load())/float64(b.N), "proof-passes/op")
			b.ReportMetric(float64(work.Enumerations.Load())/float64(b.N), "proof-enums/op")
		})
	}
}
