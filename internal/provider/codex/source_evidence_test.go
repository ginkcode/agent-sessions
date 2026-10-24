package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// Generated multiple-rollout workloads only; no private stores. Large ignored
// records model byte-heavy sources, not the small-message microbenchmark.
func sourceEvidenceFixture(b testing.TB, mib int) (string, model.SessionRef) {
	b.Helper()
	root := b.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "09", "28")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	padding := record("world_state", `{"snapshot":"`+strings.Repeat("x", 8192)+`"}`)
	for i := 0; i < 8; i++ {
		content := record("session_meta", fmt.Sprintf(`{"id":"evidence-%d"}`, i)) + strings.Repeat(padding, (mib<<20)/8/len(padding)) + record("response_item", `{"type":"message","role":"user","content":"prompt"}`)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("rollout-%d.jsonl", i)), []byte(content), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	return root, model.SessionRef{Agent: model.AgentCodex, ID: "evidence-0"}
}

func BenchmarkSourceLarge(b *testing.B) {
	for _, mib := range []int{38, 100} {
		b.Run(fmt.Sprintf("%dMiB", mib), func(b *testing.B) {
			root, ref := sourceEvidenceFixture(b, mib)
			ctx := context.Background()
			tr, err := newExperimentalSource(root, nil).Load(ctx, ref)
			if err != nil {
				b.Fatal(err)
			}
			for _, mode := range []string{"OrdinaryScanBefore", "OrdinaryScanAfter", "StandaloneLoad", "ProductionBypass", "Cold", "Warm", "Changed"} {
				b.Run(mode, func(b *testing.B) {
					p := newExperimentalSource(root, nil)
					if mode == "Warm" || mode == "Changed" || mode == "OrdinaryScanAfter" {
						if _, err := p.LoadFromSource(ctx, ref, tr.Meta); err != nil {
							b.Fatal(err)
						}
					}
					var proof SourceWork
					var scan provider.ScanWork
					observed := WithSourceWork(provider.WithScanWork(ctx, &scan), &proof)
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						switch mode {
						case "OrdinaryScanBefore", "OrdinaryScanAfter":
							_, err = p.Scan(observed, provider.ScanState{})
						case "StandaloneLoad":
							_, err = p.Load(observed, ref)
						case "ProductionBypass":
							p.sourceExperiment = false
							_, err = provider.LoadWithSource(observed, p, ref, tr.Meta)
						case "Cold":
							_, err = newExperimentalSource(root, nil).LoadFromSource(observed, ref, tr.Meta)
						case "Warm":
							_, err = p.LoadFromSource(observed, ref, tr.Meta)
						case "Changed":
							// Force evidence invalidation without changing record validity.
							path := filepath.Join(root, "sessions", "2026", "09", "28", "rollout-7.jsonl")
							f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
							if e != nil {
								b.Fatal(e)
							}
							_, e = f.WriteString("\n")
							_ = f.Close()
							if e != nil {
								b.Fatal(e)
							}
							_, err = provider.LoadWithSource(observed, p, ref, tr.Meta)
						}
						if err != nil {
							b.Fatal(err)
						}
					}
					b.StopTimer()
					for name, value := range map[string]int64{"proof-B/op": proof.Bytes.Load(), "proof-passes/op": proof.Passes.Load(), "scan-records/op": scan.Records.Load(), "scan-B/op": scan.RecordBytes.Load(), "scan-opens/op": scan.SourceOpens.Load()} {
						b.ReportMetric(float64(value)/float64(b.N), name)
					}
				})
			}
		})
	}
}

func TestSourceRetainedHeapSample(t *testing.T) {
	if testing.Short() {
		t.Skip("explicit retained heap evidence")
	}
	root, ref := sourceEvidenceFixture(t, 100)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	p := newExperimentalSource(root, nil)
	ctx := context.Background()
	tr, err := p.LoadFromSource(ctx, ref, model.SessionMeta{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Meta.Ref != ref {
		t.Fatal("unexpected transcript")
	}
	tr = nil
	runtime.GC()
	runtime.ReadMemStats(&after)
	if p.sourceSnapshot == nil {
		t.Fatal("missing retained snapshot")
	}
	t.Logf("100MiB fixture: retained HeapAlloc delta=%d bytes, sources=%d canonical=%d (process-global GC sample, not peak or strict bound)", int64(after.HeapAlloc)-int64(before.HeapAlloc), len(p.sourceSnapshot.proof.files), len(p.sourceSnapshot.canonical))
	runtime.KeepAlive(p)
	runtime.KeepAlive(tr)
}
