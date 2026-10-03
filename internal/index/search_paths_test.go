package index_test

import (
	"testing"

	"github.com/ginkcode/agent-sessions/internal/index"
	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
)

func TestSearchWindowsDirectories(t *testing.T) {
	db, err := index.Open(t.Context(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fake := providertest.NewFake(model.AgentCodex, "Codex")
	meta := model.SessionMeta{
		Ref: model.SessionRef{Agent: model.AgentCodex, ID: "windows"},
		CWD: `C:\Users\Dev\project\sub`, RepoRoot: `C:\Users\Dev\project`,
	}
	fake.Transcripts[meta.Ref.ID] = &model.Transcript{
		Meta: meta,
		Messages: []model.Message{{
			Role:  model.RoleUser,
			Parts: []model.Part{{Kind: model.PartText, Text: "SQLite path search"}},
		}},
	}
	if err := db.CommitScan(t.Context(), meta.Ref.Agent, provider.ScanResult{Changed: []model.SessionMeta{meta}}); err != nil {
		t.Fatal(err)
	}
	if err := db.IndexPending(t.Context(), provider.Set{fake}, nil); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]bool{
		`C:\Users\Dev\project\sub`:  true,
		`c:\users\dev\PROJECT\SUB`:  true,
		`C:/Users/Dev/project/sub/`: true,
		`\\?\C:\Users\Dev\project`:  true,
		`C:\Users\Dev\project`:      true,
		`c:\users\DEV`:              true,
		`C:\`:                       true,
		`C:\Users\Dev\proj`:         false,
		`C:\Users\Dev\project2`:     false,
		`D:\Users\Dev\project`:      false,
		`/Users/Dev/project`:        false,
	} {
		hits, err := db.Search(t.Context(), "SQLite", index.SearchFilter{Dir: dir})
		if err != nil {
			t.Fatalf("Search(%q): %v", dir, err)
		}
		if got := len(hits) == 1; got != want {
			t.Errorf("Search(%q) matched = %t, want %t; hits = %+v", dir, got, want, hits)
		}
	}
}
