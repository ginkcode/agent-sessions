package index

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func catalogFixture(tb testing.TB, n int) (string, []model.SessionMeta) {
	tb.Helper()
	root := tb.TempDir()
	metas := make([]model.SessionMeta, n)
	now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for i := range metas {
		metas[i] = model.SessionMeta{
			Ref:        model.SessionRef{Agent: model.AgentClaude, ID: fmt.Sprintf("synthetic-%05d", i)},
			SourcePath: filepath.Join(root, "sources", fmt.Sprintf("%05d.jsonl", i)),
			CWD:        root, RepoRoot: root, Title: fmt.Sprintf("Synthetic session %d", i),
			FirstPrompt: "Synthetic catalog benchmark", Model: "synthetic-model",
			CreatedAt: now.Add(-time.Duration(i) * time.Minute), UpdatedAt: now.Add(-time.Duration(i) * time.Second),
			Counts: model.MessageCounts{User: 5, Assistant: 5, ToolCalls: 10}, Tokens: model.TokenUsage{Input: 1000, Output: 500},
		}
	}
	db, err := Open(context.Background(), root, "")
	if err != nil {
		tb.Fatal(err)
	}
	err = db.CommitScan(context.Background(), model.AgentClaude, provider.ScanResult{Changed: metas, State: provider.ScanState{Cursor: "synthetic"}})
	closeErr := db.Close()
	if err != nil {
		tb.Fatal(err)
	}
	if closeErr != nil {
		tb.Fatal(closeErr)
	}
	return root, metas
}

func restoreCatalog(tb testing.TB, db *DB, n int) *scan.Catalog {
	tb.Helper()
	metas, err := db.LoadCatalog(context.Background())
	if err != nil {
		tb.Fatal(err)
	}
	cat := scan.NewCatalog()
	cat.Reset(metas)
	if len(cat.All()) != n {
		tb.Fatalf("restored %d rows, want %d", len(cat.All()), n)
	}
	return cat
}

func TestSyntheticCatalogBaseline(t *testing.T) {
	for _, n := range []int{1000, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			root, want := catalogFixture(t, n)
			db, err := Open(context.Background(), root, "")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			cat := restoreCatalog(t, db, n)
			all := cat.All()
			for i, meta := range all {
				if meta != want[i] {
					t.Fatalf("row %d mismatch: got %+v want %+v", i, meta, want[i])
				}
			}
		})
	}
}

func BenchmarkSyntheticCatalog(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for _, mode := range []string{"open", "restore", "open-restore"} {
				b.Run(mode, func(b *testing.B) {
					root, _ := catalogFixture(b, n)
					var db *DB
					if mode == "restore" {
						var err error
						db, err = Open(context.Background(), root, "")
						if err != nil {
							b.Fatal(err)
						}
						defer func() { _ = db.Close() }()
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						current := db
						if mode != "restore" {
							var err error
							current, err = Open(context.Background(), root, "")
							if err != nil {
								b.Fatal(err)
							}
						}
						if mode != "open" {
							restoreCatalog(b, current, n)
						}
						if mode != "restore" {
							// Closing is lifecycle cleanup, not database-open work.
							b.StopTimer()
							if err := current.Close(); err != nil {
								b.Fatal(err)
							}
							b.StartTimer()
						}
					}
				})
			}
		})
	}
}
