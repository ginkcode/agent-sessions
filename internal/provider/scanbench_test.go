package provider_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/claude"
	"github.com/ginkcode/agent-sessions/internal/provider/codex"
)

const (
	scanSources = 64
	scanRecords = 32
)

func scanFixture(tb testing.TB, agent string) (provider.Provider, string, string, int64) {
	tb.Helper()
	root := tb.TempDir()
	var p provider.Provider
	if agent == "claude" {
		p = claude.New(root, nil)
	} else {
		p = codex.New(root, nil)
	}
	var first, appendRecord string
	var size int64
	for i := range scanSources {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", i)
		dir := filepath.Join(root, "projects", "synthetic")
		name := id + ".jsonl"
		body := ""
		if agent == "codex" {
			dir = filepath.Join(root, "sessions", "2026", "10", "24")
			name = "rollout-2026-10-24T00-00-00-" + id + ".jsonl"
			body = fmt.Sprintf(`{"timestamp":"2026-10-24T00:00:00Z","type":"session_meta","payload":{"id":%q}}`+"\n", id)
		}
		for j := range scanRecords {
			var record string
			if agent == "claude" {
				record = fmt.Sprintf(`{"type":"user","uuid":"u-%d","timestamp":"2026-10-24T00:00:01Z","message":{"role":"user","content":"Synthetic prompt %d"}}`+"\n", j, j)
			} else {
				record = fmt.Sprintf(`{"timestamp":"2026-10-24T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Synthetic prompt %d"}]}}`+"\n", j)
			}
			body += record
			appendRecord = record
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			tb.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			tb.Fatal(err)
		}
		if i == 0 {
			first = path
		}
		size += int64(len(body))
	}
	return p, first, appendRecord, size
}

func checkedScan(tb testing.TB, p provider.Provider, prev provider.ScanState, work *provider.ScanWork, changed int) provider.ScanResult {
	tb.Helper()
	result, err := p.Scan(provider.WithScanWork(context.Background(), work), prev)
	if err != nil {
		tb.Fatal(err)
	}
	if len(result.State.Sources) != scanSources || len(result.Changed) != changed || len(result.Removed) != 0 || result.Diag.ParseErrors != 0 {
		tb.Fatalf("invalid scan: sources=%d changed=%d removed=%d diagnostics=%+v", len(result.State.Sources), len(result.Changed), len(result.Removed), result.Diag)
	}
	return result
}

func appendSource(tb testing.TB, path, record string) {
	tb.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		tb.Fatal(err)
	}
	_, err = f.WriteString(record)
	closeErr := f.Close()
	if err != nil {
		tb.Fatal(err)
	}
	if closeErr != nil {
		tb.Fatal(closeErr)
	}
}

func TestSyntheticScanBaseline(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			p, path, record, _ := scanFixture(t, agent)
			coldWork := new(provider.ScanWork)
			cold := checkedScan(t, p, provider.ScanState{}, coldWork, scanSources)
			for _, meta := range cold.Changed {
				if meta.Counts != (model.MessageCounts{User: scanRecords}) || meta.FirstPrompt != "Synthetic prompt 0" {
					t.Fatalf("bad fixture metadata: %+v", meta)
				}
			}
			warmWork := new(provider.ScanWork)
			warm := checkedScan(t, p, cold.State, warmWork, 0)
			appendSource(t, path, record)
			appendWork := new(provider.ScanWork)
			appended := checkedScan(t, p, warm.State, appendWork, 1)
			if appended.Changed[0].Counts.User != scanRecords+1 {
				t.Fatal("append did not increment exactly once")
			}
			for _, tc := range []struct {
				w                                *provider.ScanWork
				opens, records, decodes, encodes int64
			}{
				{coldWork, scanSources, scanSources * scanRecords, 0, scanSources},
				{warmWork, 0, 0, scanSources, 0},
				{appendWork, 1, 1, scanSources, 1},
			} {
				if agent == "claude" && tc.w != coldWork {
					tc.decodes = 0
					if tc.w == appendWork {
						tc.decodes = 1
					}
				}
				if agent == "codex" {
					tc.decodes += scanSources // canonical checkpoint pass
					tc.encodes += scanSources
					if tc.w != coldWork {
						tc.decodes += scanSources
					} // previous canonical metadata
					if tc.w == coldWork {
						tc.records += scanSources
					} // session_meta
				}
				if tc.w.SourceOpens.Load() != tc.opens || tc.w.Records.Load() != tc.records || tc.w.CheckpointDecodes.Load() != tc.decodes || tc.w.CheckpointEncodes.Load() != tc.encodes {
					t.Fatalf("work got opens/records/decode/encode=%d/%d/%d/%d want %d/%d/%d/%d", tc.w.SourceOpens.Load(), tc.w.Records.Load(), tc.w.CheckpointDecodes.Load(), tc.w.CheckpointEncodes.Load(), tc.opens, tc.records, tc.decodes, tc.encodes)
				}
			}
			checkedScan(t, p, appended.State, new(provider.ScanWork), 0)
		})
	}
}

func BenchmarkSyntheticScan(b *testing.B) {
	for _, agent := range []string{"claude", "codex"} {
		b.Run(agent, func(b *testing.B) {
			for _, mode := range []string{"cold", "unchanged", "one-source-append"} {
				b.Run(mode, func(b *testing.B) {
					p, path, record, size := scanFixture(b, agent)
					prev := provider.ScanState{}
					changed := scanSources
					if mode != "cold" {
						prev = checkedScan(b, p, prev, new(provider.ScanWork), scanSources).State
						changed = 0
					}
					if mode == "one-source-append" {
						appendSource(b, path, record)
						changed = 1
					}
					work := new(provider.ScanWork)
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						checkedScan(b, p, prev, work, changed)
					}
					b.StopTimer()
					b.ReportMetric(float64(size), "fixture-B")
					for name, n := range map[string]int64{"source-opens/op": work.SourceOpens.Load(), "records/op": work.Records.Load(), "record-B/op": work.RecordBytes.Load(), "cp-decodes/op": work.CheckpointDecodes.Load(), "cp-encodes/op": work.CheckpointEncodes.Load()} {
						b.ReportMetric(float64(n)/float64(b.N), name)
					}
				})
			}
		})
	}
}
