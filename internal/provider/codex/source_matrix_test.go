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

// Each branch independently exercises stale Load and stale Blob, then a cold
// rebuild and a directly accepting Load/Blob with zero metadata parsing.
func TestSourceMutationMatrix(t *testing.T) {
	for _, mode := range []string{"wal", "numeric-add", "numeric-remove", "numeric-replace", "corrupt", "override", "override-id-mismatch", "archive-column", "parent-remove", "parent-hidden", "edges-remove", "raw-newer", "raw-active-tie", "raw-lexical-tie", "append"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			path := indexRollout(t, root, indexID, mode == "archive-column")
			original := mustRead(t, path)
			key := fmt.Sprintf("rec:%d:0", len(original))
			attachment := record("response_item", `{"type":"message","role":"user","content":[{"type":"input_file","file_data":"data:text/plain;base64,SEVMTE8="}]}`)
			if err := os.WriteFile(path, append(original, []byte(attachment)...), 0o600); err != nil {
				t.Fatal(err)
			}
			parent := indexRollout(t, root, childID, false)
			db := stateDB(t, root, "state_9.sqlite", stateSQL)
			insertThread(t, db, indexID, path, "original", 0)
			insertThread(t, db, childID, parent, "parent", 0)
			if _, err := db.Exec(`INSERT INTO thread_spawn_edges VALUES (?,?)`, childID, indexID); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "raw-") {
				if _, err := db.Exec(`UPDATE threads SET rollout_path='' WHERE id=?`, indexID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "wal" {
				if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0`); err != nil {
					t.Fatal(err)
				}
			}
			p := newExperimentalSource(root, nil)
			ref := model.SessionRef{Agent: model.AgentCodex, ID: indexID}
			p.sourceExperiment = true
			ctx := context.Background()
			initial, err := p.Load(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.LoadFromSource(ctx, ref, initial.Meta); err != nil {
				t.Fatal(err)
			}
			blobP := newExperimentalSource(root, nil)
			blobP.sourceSnapshot = p.sourceSnapshot
			raw := string(mustRead(t, path))
			switch mode {
			case "wal":
				if _, err := db.Exec(`UPDATE threads SET title='WAL title'`); err != nil {
					t.Fatal(err)
				}
				if info, err := os.Stat(filepath.Join(root, "state_9.sqlite-wal")); err != nil || info.Size() == 0 {
					t.Fatalf("no WAL: %v", err)
				}
			case "numeric-add":
				next := stateDB(t, root, "state_10.sqlite", stateSQL)
				insertThread(t, next, indexID, path, "numeric ten", 0)
			case "numeric-remove", "numeric-replace", "corrupt":
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(root, "state_9.sqlite")); err != nil {
					t.Fatal(err)
				}
				if mode == "numeric-replace" {
					next := stateDB(t, root, "state_9.sqlite", stateSQL)
					insertThread(t, next, indexID, path, "replacement", 0)
				}
				if mode == "corrupt" {
					if err := os.WriteFile(filepath.Join(root, "state_9.sqlite"), []byte("corrupt"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "override", "override-id-mismatch":
				alternate := filepath.Join(root, "unconventional", "rollout-selected.jsonl")
				if err := os.MkdirAll(filepath.Dir(alternate), 0o700); err != nil {
					t.Fatal(err)
				}
				if mode == "override-id-mismatch" {
					raw = strings.ReplaceAll(raw, indexID, orphanID)
				}
				if err := os.WriteFile(alternate, []byte(raw), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE threads SET rollout_path=? WHERE id=?`, alternate, indexID); err != nil {
					t.Fatal(err)
				}
			case "archive-column":
				if _, err := db.Exec(`ALTER TABLE threads DROP COLUMN archived`); err != nil {
					t.Fatal(err)
				}
			case "parent-remove":
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
			case "parent-hidden":
				// The invalid index row does not hide a discovered parent. Remove
				// that source too: an index edge alone cannot make it visible.
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE threads SET rollout_path='/outside/rollout-hidden.jsonl' WHERE id=?`, childID); err != nil {
					t.Fatal(err)
				}
			case "edges-remove":
				if _, err := db.Exec(`DROP TABLE thread_spawn_edges`); err != nil {
					t.Fatal(err)
				}
			case "raw-newer", "raw-active-tie", "raw-lexical-tie":
				alternate := filepath.Join(root, "sessions", "2026", "09", "28", "rollout-000.jsonl")
				if mode == "raw-active-tie" {
					alternate = filepath.Join(root, "archived_sessions", "rollout-tie.jsonl")
				}
				if mode == "raw-newer" {
					raw += line("2099-01-01T00:00:00Z", "event_msg", `{"type":"task_complete"}`)
				}
				if err := os.MkdirAll(filepath.Dir(alternate), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(alternate, []byte(raw), 0o600); err != nil {
					t.Fatal(err)
				}
			case "append":
				if err := os.WriteFile(path, []byte(raw+record("response_item", `{"type":"message","role":"user","content":"appended"}`)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.LoadFromSource(ctx, ref, initial.Meta); !errors.Is(err, provider.ErrStaleSource) {
				t.Fatalf("stale Load: %v", err)
			}
			if _, err := blobP.BlobFromSource(ctx, ref, initial.Meta, "rec:0:0"); !errors.Is(err, provider.ErrStaleSource) {
				t.Fatalf("stale Blob: %v", err)
			}
			want, wantErr := newExperimentalSource(root, nil).Load(ctx, ref)
			got, gotErr := provider.LoadWithSource(ctx, p, ref, initial.Meta)
			if (wantErr == nil) != (gotErr == nil) || !reflect.DeepEqual(got, want) {
				t.Fatalf("canonical mismatch: %v / %v", gotErr, wantErr)
			}
			if wantErr != nil {
				return
			}
			switch mode {
			case "wal":
				if want.Meta.Title != "WAL title" {
					t.Fatal(want.Meta)
				}
			case "numeric-add":
				if want.Meta.Title != "numeric ten" {
					t.Fatal(want.Meta)
				}
			case "numeric-replace":
				if want.Meta.Title != "replacement" {
					t.Fatal(want.Meta)
				}
			case "override":
				if filepath.Base(want.Meta.SourcePath) != "rollout-selected.jsonl" {
					t.Fatal(want.Meta)
				}
			case "override-id-mismatch":
				if want.Meta.Title == "original" {
					t.Fatal("mismatched overlay retained")
				}
			case "archive-column":
				if !want.Meta.Archived {
					t.Fatal("missing archive column lost raw archive")
				}
			case "parent-remove", "parent-hidden", "edges-remove":
				if want.Meta.ParentID != "" {
					t.Fatal("invisible parent retained")
				}
			case "raw-newer", "raw-lexical-tie":
				if filepath.Base(want.Meta.SourcePath) != "rollout-000.jsonl" {
					t.Fatal("raw selection lost under index times", want.Meta)
				}
			case "raw-active-tie":
				if filepath.Base(want.Meta.SourcePath) != filepath.Base(path) {
					t.Fatalf("active tie lost: path=%s meta=%+v", path, want.Meta)
				}
			}
			// Corruption cannot establish proof, but still resolves exactly once.
			if mode == "corrupt" {
				if p.sourceSnapshot != nil {
					t.Fatal("corrupt index retained proof")
				}
				return
			}
			var scan provider.ScanWork
			var proof SourceWork
			observed := WithSourceWork(provider.WithScanWork(ctx, &scan), &proof)
			got, err = p.LoadFromSource(observed, ref, want.Meta)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("rebuilt accepting Load: %v", err)
			}
			a, ae := p.BlobFromSource(observed, ref, want.Meta, key)
			b, be := newExperimentalSource(root, nil).Blob(ctx, ref, key)
			if ae != nil || be != nil || string(a) != "HELLO" || !reflect.DeepEqual(a, b) {
				t.Fatalf("Blob mismatch %v / %v", ae, be)
			}
			if scan.SourceOpens.Load() != 0 || scan.Records.Load() != 0 || proof.Passes.Load() != 2 || proof.Bytes.Load() == 0 {
				t.Fatalf("warm work: opens=%d records=%d passes=%d bytes=%d", scan.SourceOpens.Load(), scan.Records.Load(), proof.Passes.Load(), proof.Bytes.Load())
			}
		})
	}
}

func TestSourcePublicationOwnership(t *testing.T) {
	p, ref, _ := syntheticRollout(t, record("session_meta", `{"id":"fixture"}`))
	p.sourceExperiment = true
	ctx := context.Background()
	tr, err := p.Load(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadFromSource(ctx, ref, tr.Meta); err != nil {
		t.Fatal(err)
	}
	old := p.sourceSnapshot
	// A validator has captured old evidence, then pauses until a different
	// caller invalidates and rebuilds. Its late result must not clear new proof.
	resume := make(chan struct{})
	done := make(chan struct{})
	oldGeneration := p.sourceGeneration
	go func() {
		<-resume
		p.invalidateSource(old)
		p.publishSource(oldGeneration, old)
		close(done)
	}()
	p.invalidateSource(old)
	if _, err := p.LoadFromSource(ctx, ref, tr.Meta); err != nil {
		t.Fatal(err)
	}
	current := p.sourceSnapshot
	close(resume)
	<-done
	if p.sourceSnapshot != current {
		t.Fatal("late invalidation/publication replaced newer proof")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 4 {
				if _, err := p.Scan(ctx, provider.ScanState{}); err != nil {
					t.Error(err)
				}
				if _, err := p.LoadFromSource(ctx, ref, tr.Meta); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	if p.sourceSnapshot != current {
		t.Fatal("ordinary concurrent Scan published evidence")
	}
}

func TestSourceFreshHintClearsOldProof(t *testing.T) {
	p, ref, path := syntheticRollout(t, record("session_meta", `{"id":"fixture"}`))
	p.sourceExperiment = true
	ctx := context.Background()
	initial, err := p.Load(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadFromSource(ctx, ref, initial.Meta); err != nil {
		t.Fatal(err)
	}
	old := p.sourceSnapshot
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(record("response_item", `{"type":"message","role":"user","content":"new prompt"}`))
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := p.Load(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Meta == initial.Meta {
		t.Fatal("fixture did not change hint")
	}
	if p.sourceSnapshot != old {
		t.Fatal("standalone load changed optional evidence")
	}
	if _, err := p.LoadFromSource(ctx, ref, fresh.Meta); !errors.Is(err, provider.ErrStaleSource) {
		t.Fatal(err)
	}
	if p.sourceSnapshot != nil {
		t.Fatal("new hint left permanently stale evidence")
	}
	if _, err := p.LoadFromSource(ctx, ref, fresh.Meta); err != nil {
		t.Fatal(err)
	}
	var scan provider.ScanWork
	if _, err := p.LoadFromSource(provider.WithScanWork(ctx, &scan), ref, fresh.Meta); err != nil {
		t.Fatal(err)
	}
	if scan.SourceOpens.Load() != 0 {
		t.Fatal("rebuild did not restore acceptance")
	}
}

func TestSourceCountCap(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "archived_sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= maxProofSources; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("rollout-%d.jsonl", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Production declines repeatedly before even enumerating this over-cap root.
	ordinary := New(root, nil)
	var bypass SourceWork
	ctx := WithSourceWork(context.Background(), &bypass)
	ref := model.SessionRef{Agent: model.AgentCodex, ID: "missing"}
	for range 5 {
		if _, err := ordinary.LoadFromSource(ctx, ref, model.SessionMeta{Ref: ref}); !errors.Is(err, provider.ErrStaleSource) {
			t.Fatal(err)
		}
		if _, err := ordinary.BlobFromSource(ctx, ref, model.SessionMeta{Ref: ref}, "invalid"); !errors.Is(err, provider.ErrStaleSource) {
			t.Fatal(err)
		}
	}
	assertNoSourceWork(t, &bypass)
	var work SourceWork
	p := newExperimentalSource(root, nil)
	if p.proveSources(WithSourceWork(context.Background(), &work)) != nil {
		t.Fatal("oversized source set retained")
	}
	if work.Passes.Load() != 1 || work.Bytes.Load() != 0 || work.Opens.Load() != 0 {
		t.Fatal("source cap did not short circuit digest work")
	}
}

func TestSourceLazyAndFinalTails(t *testing.T) {
	for _, tail := range []string{"newline", "valid", "invalid"} {
		t.Run(tail, func(t *testing.T) {
			output := strings.Repeat("x", 100000)
			last := record("response_item", fmt.Sprintf(`{"type":"function_call_output","call_id":"big","output":%q}`, output))
			if tail != "newline" {
				last = strings.TrimSuffix(last, "\n")
			}
			if tail == "invalid" {
				last = last[:len(last)-5]
			}
			p, ref, _ := syntheticRollout(t, record("session_meta", `{"id":"fixture"}`), record("response_item", `{"type":"function_call","call_id":"big","name":"run","arguments":"{}"}`), last)
			p.sourceExperiment = true
			ctx := context.Background()
			want, err := p.Load(ctx, ref)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.LoadFromSource(ctx, ref, want.Meta); err != nil {
				t.Fatal(err)
			}
			got, err := p.LoadFromSource(ctx, ref, want.Meta)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("tail Load %v", err)
			}
			for _, msg := range want.Messages {
				for _, part := range msg.Parts {
					if part.Tool != nil && part.Tool.OutputRef != "" {
						a, ae := p.BlobFromSource(ctx, ref, want.Meta, part.Tool.OutputRef)
						b, be := p.Blob(ctx, ref, part.Tool.OutputRef)
						if ae != nil || be != nil || string(a) != output || !reflect.DeepEqual(a, b) {
							t.Fatalf("tail Blob %v / %v", ae, be)
						}
					}
				}
			}
		})
	}
}
