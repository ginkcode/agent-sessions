package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// Only tests can opt into the unaccepted full-byte reconciliation experiment.
func newExperimentalSource(root string, git *pathutil.GitResolver) *Provider {
	p := New(root, git)
	p.sourceExperiment = true
	return p
}

func assertNoSourceWork(t testing.TB, proof *SourceWork) {
	t.Helper()
	if proof.Bytes.Load() != 0 || proof.Passes.Load() != 0 || proof.Enumerations.Load() != 0 || proof.Opens.Load() != 0 || proof.IndexReads.Load() != 0 {
		t.Fatalf("unexpected proof work: %+v", proof)
	}
}

func TestSourceProductionBypass(t *testing.T) {
	p, ref, path := syntheticRollout(t, record("session_meta", `{"id":"fixture"}`), record("response_item", `{"type":"message","role":"user","content":[{"type":"input_file","file_data":"data:text/plain;base64,SEVMTE8="}]}`))
	ctx := context.Background()
	initial, err := p.Load(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	key := initial.Messages[0].Parts[0].File.Ref
	// Even retained experimental evidence cannot activate a default provider.
	experiment := newExperimentalSource(p.root, nil)
	if _, err := experiment.LoadFromSource(ctx, ref, initial.Meta); err != nil {
		t.Fatal(err)
	}
	p.sourceSnapshot = experiment.sourceSnapshot
	var proof SourceWork
	var scan provider.ScanWork
	observed := WithSourceWork(provider.WithScanWork(ctx, &scan), &proof)
	for range 3 {
		if _, err := p.LoadFromSource(observed, ref, initial.Meta); !errors.Is(err, provider.ErrStaleSource) {
			t.Fatal(err)
		}
		if _, err := p.BlobFromSource(observed, ref, initial.Meta, key); !errors.Is(err, provider.ErrStaleSource) {
			t.Fatal(err)
		}
	}
	if scan.SourceOpens.Load() != 0 {
		t.Fatal("decline performed canonical scan")
	}
	for i := range 3 {
		// Rapid mutation never causes proof/rebuild cycles in production.
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(record("response_item", `{"type":"message","role":"user","content":"new"}`))
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("append: %v / %v", err, closeErr)
		}
		want, err := p.Load(ctx, ref)
		if err != nil {
			t.Fatal(err)
		}
		got, err := provider.LoadWithSource(observed, p, ref, initial.Meta)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("load %d: %v", i, err)
		}
		gotBlob, err := provider.BlobWithSource(observed, p, ref, initial.Meta, key)
		if err != nil || string(gotBlob) != "HELLO" {
			t.Fatalf("blob: %q / %v", gotBlob, err)
		}
	}
	// One Scan for each Load and each Blob fallback, not a hidden cold Scan.
	if scan.SourceOpens.Load() != 6 {
		t.Fatalf("fallback scans=%d", scan.SourceOpens.Load())
	}
	assertNoSourceWork(t, &proof)
	canceled, cancel := context.WithCancel(observed)
	cancel()
	if _, err := p.LoadFromSource(canceled, ref, initial.Meta); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.BlobFromSource(canceled, ref, initial.Meta, key); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := provider.LoadWithSource(observed, p, ref, initial.Meta); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	assertNoSourceWork(t, &proof)
}

func TestSourceProductionOversizeBypass(t *testing.T) {
	p, ref, path := syntheticRollout(t, record("session_meta", `{"id":"fixture"}`))
	f, err := os.OpenFile(path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxProofBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	var proof SourceWork
	var scan provider.ScanWork
	ctx := WithSourceWork(provider.WithScanWork(context.Background(), &scan), &proof)
	for range 5 {
		if _, err := p.LoadFromSource(ctx, ref, model.SessionMeta{Ref: ref}); !errors.Is(err, provider.ErrStaleSource) {
			t.Fatal(err)
		}
		if _, err := p.BlobFromSource(ctx, ref, model.SessionMeta{Ref: ref}, "invalid"); !errors.Is(err, provider.ErrStaleSource) {
			t.Fatal(err)
		}
	}
	assertNoSourceWork(t, &proof)
	if scan.SourceOpens.Load() != 0 || p.sourceSnapshot != nil {
		t.Fatal("oversized bypass performed cold work")
	}
}

func BenchmarkSourceBlobPolicy(b *testing.B) {
	for _, mib := range []int{1, 38, 100} {
		b.Run(fmt.Sprintf("%dMiB", mib), func(b *testing.B) {
			root, ref := sourceEvidenceFixture(b, mib)
			p := New(root, nil)
			ctx := context.Background()
			tr, err := p.Load(ctx, ref)
			if err != nil {
				b.Fatal(err)
			}
			// Fixture's user message is at this byte offset; replaced below with
			// an attachment so the benchmark extracts a real, identical Blob.
			path := tr.Meta.SourcePath
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				b.Fatal(err)
			}
			info, err := f.Stat()
			if err != nil {
				b.Fatal(err)
			}
			key := fmt.Sprintf("rec:%d:0", info.Size())
			_, err = f.WriteString(record("response_item", `{"type":"message","role":"user","content":[{"type":"input_file","file_data":"data:text/plain;base64,SEVMTE8="}]}`))
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				b.Fatalf("fixture append: %v / %v", err, closeErr)
			}
			for _, mode := range []string{"Standalone", "ProductionBypass"} {
				b.Run(mode, func(b *testing.B) {
					var proof SourceWork
					var scan provider.ScanWork
					observed := WithSourceWork(provider.WithScanWork(ctx, &scan), &proof)
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						var data []byte
						var err error
						if mode == "Standalone" {
							data, err = p.Blob(observed, ref, key)
						} else {
							data, err = provider.BlobWithSource(observed, p, ref, tr.Meta, key)
						}
						if err != nil || string(data) != "HELLO" {
							b.Fatalf("blob: %q / %v", data, err)
						}
					}
					b.StopTimer()
					assertNoSourceWork(b, &proof)
					b.ReportMetric(float64(proof.Bytes.Load())/float64(b.N), "proof-B/op")
					b.ReportMetric(float64(scan.SourceOpens.Load())/float64(b.N), "scan-opens/op")
				})
			}
		})
	}
}

func TestSourceExperimentalColdBlobBypass(t *testing.T) {
	p, ref, _ := syntheticRollout(t, record("session_meta", `{"id":"fixture"}`))
	p.sourceExperiment = true
	var proof SourceWork
	var scan provider.ScanWork
	ctx := WithSourceWork(provider.WithScanWork(context.Background(), &scan), &proof)
	for range 5 {
		if _, err := p.BlobFromSource(ctx, ref, model.SessionMeta{Ref: ref}, "rec:0:0"); !errors.Is(err, provider.ErrStaleSource) {
			t.Fatal(err)
		}
	}
	assertNoSourceWork(t, &proof)
	if scan.SourceOpens.Load() != 0 || p.sourceSnapshot != nil {
		t.Fatal("cold Blob established proof")
	}
}
