package opencode

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/testutil/golden"
)

func TestScanV1Only(t *testing.T) {
	f := newScanFixture(t)
	if _, err := f.db.Exec(`DROP TABLE session_v2; DROP TABLE session_message`); err != nil {
		t.Fatal(err)
	}
	f.v1Session("v1", map[string]any{"title": nil})
	f.v1Message("m-user", "v1", "user", 1_700_000_000_001)
	f.v1Part("p-text", "m-user", "v1", 1_700_000_000_002, `{"type":"text","text":"first prompt"}`)
	f.v1Message("m-synthetic", "v1", "user", 1_700_000_000_003)
	f.v1Part("p-synthetic", "m-synthetic", "v1", 1_700_000_000_004, `{"type":"text","text":"generated","synthetic":true}`)
	f.v1Message("m-compact", "v1", "user", 1_700_000_000_005)
	f.v1Part("p-compact", "m-compact", "v1", 1_700_000_000_006, `{"type":"compaction","auto":true}`)
	f.v1Message("m-assistant", "v1", "assistant", 1_700_000_000_007)
	f.v1Part("p-tool", "m-assistant", "v1", 1_700_000_000_008, `{"type":"tool","tool":"bash","callID":"c1","state":{"status":"completed","input":{},"output":"ok"}}`)
	f.v1Message("m-follow-up", "v1", "user", 1_700_000_000_009)
	f.v1Part("p-follow-up", "m-follow-up", "v1", 1_700_000_000_010, `{"type":"text","text":"latest prompt"}`)
	f.v1Message("m-attach", "v1", "user", 1_700_000_000_011)
	f.v1Part("p-attach", "m-attach", "v1", 1_700_000_000_012, `{"type":"file","mime":"image/png","url":"data:image/png;base64,AA=="}`)

	result, err := New(f.root, nil).Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) != 1 {
		t.Fatalf("changed = %+v", result.Changed)
	}
	meta := result.Changed[0]
	if meta.Ref.ID != "v1" || meta.Title != "latest prompt" || meta.FirstPrompt != "first prompt" || meta.Counts != (model.MessageCounts{User: 3, Assistant: 1, ToolCalls: 1}) {
		t.Fatalf("v1 meta = %+v", meta)
	}
	var checkpoint dbCheckpoint
	if err := json.Unmarshal(result.State.Sources[filepath.Join(f.root, dbName)].Checkpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.Version != dbCheckpointVersion || checkpoint.Gen["v1"] != GenV1 {
		t.Fatalf("checkpoint = %+v", checkpoint)
	}
}

func TestScanV1Golden(t *testing.T) {
	f := newScanFixture(t)
	if _, err := f.db.Exec(`DROP TABLE session_v2; DROP TABLE session_message`); err != nil {
		t.Fatal(err)
	}
	f.project("proj", "/source/main")
	f.v1Session("alpha", map[string]any{
		"project_id": "proj", "directory": "/source/worktree", "title": nil,
		"cost": 2.5, "tokens_input": 40, "tokens_output": 20,
		"tokens_reasoning": 5, "tokens_cache_read": 7, "tokens_cache_write": 3,
	})
	f.v1Message("m1", "alpha", "user", 1_700_000_000_001)
	f.v1Part("p1", "m1", "alpha", 1_700_000_000_002, `{"type":"text","text":"  First   v1\n prompt  "}`)
	f.v1Message("m2", "alpha", "assistant", 1_700_000_000_003)
	f.v1Part("p2", "m2", "alpha", 1_700_000_000_004, `{"type":"tool","tool":"read","callID":"call-1","state":{"status":"completed","input":{},"output":"done"}}`)
	f.v1Session("child", map[string]any{"parent_id": "alpha", "title": nil, "model": nil, "time_archived": int64(1_700_000_200_000)})

	result, err := New(f.root, nil).Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range result.Changed {
		result.Changed[i].SourcePath = "<fixture>/opencode.db"
	}
	golden.JSON(t, "scan_v1", result.Changed)
}

func TestScanGenerationMerge(t *testing.T) {
	t.Run("v2 wins tie", func(t *testing.T) {
		f := newScanFixture(t)
		f.session("same", map[string]any{"title": "v2 title"})
		f.v1Session("same", map[string]any{"title": "v1 title"})
		result, err := New(f.root, nil).Scan(t.Context(), provider.ScanState{})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Changed) != 1 || result.Changed[0].Title != "v2 title" {
			t.Fatalf("tie merge = %+v", result.Changed)
		}
	})

	t.Run("newer v1 wins", func(t *testing.T) {
		f := newScanFixture(t)
		f.session("same", map[string]any{"title": "v2 title", "time_updated": int64(100)})
		f.v1Session("same", map[string]any{"title": "v1 title", "time_updated": int64(101)})
		result, err := New(f.root, nil).Scan(t.Context(), provider.ScanState{})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Changed) != 1 || result.Changed[0].Title != "v1 title" {
			t.Fatalf("newer merge = %+v", result.Changed)
		}
	})
}

func TestScanUnionRemovalAndV1Incremental(t *testing.T) {
	f := newScanFixture(t)
	f.session("both", nil)
	f.v1Session("both", nil)
	f.v1Session("v1", nil)
	p := New(f.root, nil)
	first, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Changed) != 2 {
		t.Fatalf("first = %+v", first.Changed)
	}
	if _, err := f.db.Exec(`UPDATE session SET title='updated', time_updated=? WHERE id='v1'`, int64(1_700_000_300_000)); err != nil {
		t.Fatal(err)
	}
	second, err := p.Scan(t.Context(), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Changed) != 1 || second.Changed[0].Ref.ID != "v1" || second.Changed[0].Title != "updated" {
		t.Fatalf("v1 incremental = %+v", second.Changed)
	}
	if _, err := f.db.Exec(`DELETE FROM session_v2 WHERE id='both'; DELETE FROM session WHERE id='both'`); err != nil {
		t.Fatal(err)
	}
	third, err := p.Scan(t.Context(), second.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Removed) != 1 || third.Removed[0].ID != "both" {
		t.Fatalf("union removal = %+v", third.Removed)
	}
}

func TestScanVersionOneCheckpointForcesFullRescan(t *testing.T) {
	f := newScanFixture(t)
	f.session("s1", nil)
	path := filepath.Join(f.root, dbName)
	state := provider.ScanState{
		Cursor: "1700000100000",
		Sources: map[string]provider.SourceState{
			path: {Checkpoint: json.RawMessage(`{"version":1,"metas":{"old":{}}}`)},
		},
	}
	result, err := New(f.root, nil).Scan(t.Context(), state)
	if err != nil {
		t.Fatal(err)
	}
	// "old" was deleted before the upgrade; the Version 1 ID list must still
	// drive its removal.
	if len(result.Changed) != 1 || result.Changed[0].Ref.ID != "s1" || len(result.Removed) != 1 || result.Removed[0].ID != "old" {
		t.Fatalf("version 1 rebuild = %+v", result)
	}
}

func TestScanVersionOneCheckpointRetainsWhenDatabaseMissing(t *testing.T) {
	root := t.TempDir()
	state := provider.ScanState{
		Cursor: "1700000100000",
		Sources: map[string]provider.SourceState{
			filepath.Join(root, dbName): {Checkpoint: json.RawMessage(`{"version":1,"metas":{"s1":{}}}`)},
		},
	}
	if _, err := New(root, nil).Scan(t.Context(), state); err == nil {
		t.Fatal("missing database after upgrade did not retain previous sessions")
	}
}

func TestScanV1BaseSchemaWithoutUsageColumns(t *testing.T) {
	f := newScanFixture(t)
	f.v1Session("old", nil)
	f.v1Message("m1", "old", "user", 1)
	f.v1Part("p1", "m1", "old", 1, `{"type":"text","text":"hello"}`)
	// OpenCode 1.2.0 through 1.14.48 predate agent/model and cost/tokens.
	for _, column := range []string{"agent", "model", "cost", "tokens_input", "tokens_output", "tokens_reasoning", "tokens_cache_read", "tokens_cache_write"} {
		if _, err := f.db.Exec(`ALTER TABLE session DROP COLUMN ` + column); err != nil {
			t.Fatal(err)
		}
	}
	f.session("v2", nil)
	p := New(f.root, nil)
	result, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) != 2 {
		t.Fatalf("changed = %+v", result.Changed)
	}
	if _, err := p.Load(t.Context(), model.SessionRef{Agent: model.AgentOpenCode, ID: "old"}); err != nil {
		t.Fatal(err)
	}
}

func TestScanIncrementalRemergesOldTimestampCopy(t *testing.T) {
	f := newScanFixture(t)
	f.v1Session("migrated", map[string]any{"title": "v1 title", "time_updated": int64(100)})
	f.session("recent", map[string]any{"time_updated": int64(1_700_000_300_000)})
	p := New(f.root, nil)
	first, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	// OpenCode's v1-to-v2 migration copies the row and keeps its timestamp,
	// which is far below the cursor.
	f.session("migrated", map[string]any{"title": "v2 title", "time_updated": int64(100)})
	second, err := p.Scan(t.Context(), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Changed) != 1 || second.Changed[0].Ref.ID != "migrated" || second.Changed[0].Title != "v2 title" {
		t.Fatalf("migrated copy = %+v", second.Changed)
	}
}

func TestScanV1WithoutPartWarnsAndKeepsV2(t *testing.T) {
	f := newScanFixture(t)
	f.session("v2", nil)
	f.v1Session("v1", nil)
	if _, err := f.db.Exec(`DROP TABLE part`); err != nil {
		t.Fatal(err)
	}
	result, err := New(f.root, nil).Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) != 1 || result.Changed[0].Ref.ID != "v2" || len(result.Diag.Warnings) == 0 {
		t.Fatalf("partial v1 schema = %+v", result)
	}
}

func TestScanV2SchemaDroppedRetainsV2OnlySessions(t *testing.T) {
	f := newScanFixture(t)
	f.session("v2only", nil)
	f.session("both", nil)
	f.v1Session("both", nil)
	p := New(f.root, nil)
	first, err := p.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DROP TABLE session_message; DROP TABLE session_v2`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Scan(t.Context(), first.State); err == nil {
		t.Fatal("v2-only session reported removed after the v2 tables vanished")
	}

	// Without v2-only sessions, the shared session switches to its v1 row.
	g := newScanFixture(t)
	g.session("both", nil)
	g.v1Session("both", nil)
	q := New(g.root, nil)
	first, err = q.Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.db.Exec(`DROP TABLE session_message; DROP TABLE session_v2`); err != nil {
		t.Fatal(err)
	}
	second, err := q.Scan(t.Context(), first.State)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Removed) != 0 || len(second.Changed) != 1 {
		t.Fatalf("generation switch = %+v", second)
	}
}

func TestScanLegacyOnlyWarns(t *testing.T) {
	root := t.TempDir()
	fixtureLegacy(t, root)
	result, err := New(root, nil).Scan(t.Context(), provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) != 0 || len(result.Diag.Warnings) != 1 {
		t.Fatalf("legacy-only scan = %+v", result)
	}
	empty, err := New(t.TempDir(), nil).Scan(t.Context(), provider.ScanState{})
	if err != nil || len(empty.Diag.Warnings) != 0 {
		t.Fatalf("empty root scan = %+v, %v", empty, err)
	}
}
