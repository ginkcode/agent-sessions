package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
	"github.com/ginkcode/agent-sessions/internal/scan"
)

func TestLRU_Operations(t *testing.T) {
	c := newLRU[string, int](2)
	if c.len() != 0 {
		t.Fatalf("len = %d, want 0", c.len())
	}
	if _, ok := c.get("a"); ok {
		t.Error("unexpected hit for 'a'")
	}

	c.put("a", 1)
	c.put("b", 2)
	if c.len() != 2 {
		t.Fatalf("len = %d, want 2", c.len())
	}

	// Access 'a' so 'b' becomes the oldest
	if v, ok := c.get("a"); !ok || v != 1 {
		t.Errorf("get('a') = (%v, %v), want (1, true)", v, ok)
	}

	// Put 'c' should evict 'b'
	c.put("c", 3)
	if _, ok := c.get("b"); ok {
		t.Error("'b' should have been evicted")
	}
	if v, ok := c.get("a"); !ok || v != 1 {
		t.Errorf("get('a') = (%v, %v), want (1, true)", v, ok)
	}
	if v, ok := c.get("c"); !ok || v != 3 {
		t.Errorf("get('c') = (%v, %v), want (3, true)", v, ok)
	}

	// Updating existing key refreshes it and updates value
	c.put("a", 10)
	c.put("d", 4) // evicts 'c'
	if _, ok := c.get("c"); ok {
		t.Error("'c' should have been evicted")
	}
	if v, ok := c.get("a"); !ok || v != 10 {
		t.Errorf("get('a') = (%v, %v), want (10, true)", v, ok)
	}
}

func setupTestService(t *testing.T) (*Service, *providertest.Fake) {
	t.Helper()
	fake := providertest.NewFake(model.AgentClaude, "Claude Code")
	catalog := scan.NewCatalog()

	s1 := model.SessionMeta{
		Ref:         model.SessionRef{Agent: model.AgentClaude, ID: "s1"},
		Title:       "First Session",
		FirstPrompt: "Hello world",
		CWD:         "/home/user/project1",
		Model:       "claude-3-5-sonnet",
		CreatedAt:   time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 9, 28, 11, 0, 0, 0, time.UTC),
		Counts:      model.MessageCounts{User: 2, Assistant: 2},
		Tokens:      model.TokenUsage{Input: 100, Output: 50},
		CostUSD:     0.05,
		SourcePath:  "/home/user/project1/.sessions/s1.json",
	}
	s2 := model.SessionMeta{
		Ref:         model.SessionRef{Agent: model.AgentClaude, ID: "s2"},
		Title:       "Second Session",
		FirstPrompt: "Build something",
		CWD:         "/home/user/project1",
		Model:       "claude-3-5-sonnet",
		CreatedAt:   time.Date(2026, 9, 28, 9, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		Counts:      model.MessageCounts{User: 5, Assistant: 5},
		Tokens:      model.TokenUsage{Input: 500, Output: 200},
		CostUSD:     0.25,
		Live:        true,
		SourcePath:  "/home/user/project1/.sessions/s2.json",
	}
	s3 := model.SessionMeta{
		Ref:         model.SessionRef{Agent: model.AgentClaude, ID: "s3"},
		Title:       "Archived Session",
		FirstPrompt: "Old task",
		CWD:         "/home/user/project2",
		Model:       "claude-3-opus",
		CreatedAt:   time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC),
		Counts:      model.MessageCounts{User: 1, Assistant: 1},
		Archived:    true,
		SourcePath:  "/home/user/project2/.sessions/s3.json",
	}

	catalog.Apply(provider.ScanResult{
		Changed: []model.SessionMeta{s1, s2, s3},
	})

	fake.Transcripts["s1"] = &model.Transcript{
		Meta: s1,
		Messages: []model.Message{
			{ID: "m1", Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartText, Text: "Hello"}}},
			{ID: "m2", Role: model.RoleAssistant, Parts: []model.Part{{Kind: model.PartText, Text: "Hi there"}}},
			{ID: "m3", Role: model.RoleUser, Parts: []model.Part{{Kind: model.PartText, Text: "Next question"}}},
		},
	}
	fake.Blobs["claude-code:s1:text-blob"] = []byte("Plain text blob content")
	fake.Blobs["claude-code:s1:binary-blob"] = []byte{0x00, 0x01, 0x02, 0xff}

	svc := NewService(catalog, provider.Set{fake})
	return svc, fake
}

func TestListGroups(t *testing.T) {
	svc, _ := setupTestService(t)

	// Mode DirAgent with default filter (excludes archived)
	groups, err := svc.ListGroups(GroupModeDirAgent, FilterOpts{})
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("expected groups, got empty list")
	}

	// Including archived should include project2
	groupsArchived, err := svc.ListGroups(GroupModeDirAgent, FilterOpts{Archived: true})
	if err != nil {
		t.Fatalf("ListGroups with archived: %v", err)
	}
	totalSessions := 0
	for _, g := range groupsArchived {
		totalSessions += g.SessionCount
	}
	if totalSessions != 3 {
		t.Errorf("total sessions with archived = %d, want 3", totalSessions)
	}

	// Flat mode returns a node for each unarchived or archived session
	flat, err := svc.ListGroups(GroupModeFlat, FilterOpts{Archived: true})
	if err != nil {
		t.Fatalf("ListGroups flat: %v", err)
	}
	if len(flat) != 3 {
		t.Fatalf("flat groups count = %d, want 3", len(flat))
	}
	for _, n := range flat {
		if n.Kind != "session" {
			t.Errorf("flat node %q kind = %q, want session", n.Key, n.Kind)
		}
	}

	// Kind round-trips so the UI can style directories and agent groups.
	for _, tc := range []struct {
		mode        GroupMode
		root, child string
	}{
		{GroupModeDirAgent, "directory", "agent"},
		{GroupModeAgentDir, "agent", "directory"},
	} {
		nodes, err := svc.ListGroups(tc.mode, FilterOpts{})
		if err != nil {
			t.Fatalf("ListGroups %s: %v", tc.mode, err)
		}
		for _, root := range nodes {
			if root.Kind != tc.root {
				t.Errorf("%s root %q kind = %q, want %s", tc.mode, root.Key, root.Kind, tc.root)
			}
			if len(root.Children) == 0 {
				t.Fatalf("%s root %q has no children", tc.mode, root.Key)
			}
			for _, child := range root.Children {
				if child.Kind != tc.child {
					t.Errorf("%s child %q kind = %q, want %s", tc.mode, child.Key, child.Kind, tc.child)
				}
				for _, leaf := range child.Children {
					if leaf.Kind != "session" {
						t.Errorf("%s leaf %q kind = %q, want session", tc.mode, leaf.Key, leaf.Kind)
					}
				}
			}
		}
	}
}

func TestListSessions(t *testing.T) {
	svc, _ := setupTestService(t)

	// List all unarchived sessions sorted by updated desc (default)
	sessions, err := svc.ListSessions("", FilterOpts{}, SortOpts{Field: "updated", Desc: true})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions count = %d, want 2 (s1 and s2)", len(sessions))
	}
	if sessions[0].Ref.ID != "s2" {
		t.Errorf("first session = %s, want s2 (more recently updated)", sessions[0].Ref.ID)
	}

	// Filter by LiveOnly
	liveSessions, err := svc.ListSessions("", FilterOpts{LiveOnly: true}, SortOpts{})
	if err != nil {
		t.Fatalf("ListSessions live only: %v", err)
	}
	if len(liveSessions) != 1 || liveSessions[0].Ref.ID != "s2" {
		t.Errorf("live sessions = %+v, want only s2", liveSessions)
	}

	// Filter by Query
	querySessions, err := svc.ListSessions("", FilterOpts{Query: "First"}, SortOpts{})
	if err != nil {
		t.Fatalf("ListSessions query: %v", err)
	}
	if len(querySessions) != 1 || querySessions[0].Ref.ID != "s1" {
		t.Errorf("query sessions = %+v, want only s1", querySessions)
	}

	// Filter by Path (case-insensitive substring of the cwd)
	pathSessions, err := svc.ListSessions("", FilterOpts{Path: " PROJECT2 ", Archived: true}, SortOpts{})
	if err != nil {
		t.Fatalf("ListSessions path: %v", err)
	}
	if len(pathSessions) != 1 || pathSessions[0].Ref.ID != "s3" {
		t.Errorf("path sessions = %+v, want only s3", pathSessions)
	}
	groups, err := svc.ListGroups(GroupModeDirAgent, FilterOpts{Path: "project1"})
	if err != nil {
		t.Fatalf("ListGroups path: %v", err)
	}
	if len(groups) != 1 || groups[0].SessionCount != 2 {
		t.Errorf("path groups = %+v, want one project1 group with 2 sessions", groups)
	}

	// Sort by tokens asc
	sortedTokens, err := svc.ListSessions("", FilterOpts{Archived: true}, SortOpts{Field: "tokens", Desc: false})
	if err != nil {
		t.Fatalf("ListSessions sort tokens: %v", err)
	}
	tokA := sortedTokens[0].Tokens.Input + sortedTokens[0].Tokens.Output
	tokB := sortedTokens[len(sortedTokens)-1].Tokens.Input + sortedTokens[len(sortedTokens)-1].Tokens.Output
	if tokA > tokB {
		t.Error("tokens not sorted ascending")
	}
}

// TestListSessionsResolvesEveryTreeKey guards against the sidebar selecting a
// node whose key ListSessions cannot resolve ("unknown group: session:…").
func TestListSessionsResolvesEveryTreeKey(t *testing.T) {
	catalog := scan.NewCatalog()
	updated := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	parent := model.SessionMeta{
		Ref:       model.SessionRef{Agent: model.AgentOpenCode, ID: "parent"},
		Title:     "Parent",
		CWD:       "/home/user/app",
		UpdatedAt: updated,
	}
	child := model.SessionMeta{
		Ref:       model.SessionRef{Agent: model.AgentOpenCode, ID: "child"},
		Title:     "Subagent",
		CWD:       "/home/user/app",
		ParentID:  "parent",
		UpdatedAt: updated.Add(time.Minute),
	}
	other := model.SessionMeta{
		Ref:       model.SessionRef{Agent: model.AgentClaude, ID: "other"},
		Title:     "Other",
		CWD:       "/home/user/app",
		UpdatedAt: updated,
	}
	catalog.Apply(provider.ScanResult{Changed: []model.SessionMeta{parent, child, other}})
	svc := NewService(catalog, nil)

	for _, mode := range []GroupMode{GroupModeDirAgent, GroupModeAgentDir, GroupModeFlat} {
		groups, err := svc.ListGroups(mode, FilterOpts{})
		if err != nil {
			t.Fatalf("ListGroups(%s): %v", mode, err)
		}
		for _, node := range flattenGroupNodes(groups) {
			got, err := svc.ListSessions(node.Key, FilterOpts{}, SortOpts{})
			if err != nil {
				t.Errorf("mode %s: ListSessions(%q): %v", mode, node.Key, err)
				continue
			}
			if len(got) == 0 {
				t.Errorf("mode %s: ListSessions(%q) returned no sessions", mode, node.Key)
			}
		}
	}

	// HasSubagents keeps only sessions that have children.
	parents, err := svc.ListSessions("", FilterOpts{HasSubagents: true}, SortOpts{})
	if err != nil {
		t.Fatalf("ListSessions hasSubagents: %v", err)
	}
	if len(parents) != 1 || parents[0].Ref != parent.Ref {
		t.Errorf("hasSubagents sessions = %+v, want only the OpenCode parent", parents)
	}

	leaf, err := svc.ListSessions("session:"+string(model.AgentOpenCode)+":parent", FilterOpts{}, SortOpts{})
	if err != nil {
		t.Fatalf("ListSessions(parent leaf): %v", err)
	}
	ids := map[string]bool{}
	for _, m := range leaf {
		ids[m.Ref.ID] = true
	}
	if len(leaf) != 2 || !ids["parent"] || !ids["child"] {
		t.Errorf("parent leaf sessions = %v, want parent and child", ids)
	}
}

func TestAgentCountsIgnoresAgentFilter(t *testing.T) {
	svc, _ := setupTestService(t)
	svc.Catalog().Apply(provider.ScanResult{Changed: []model.SessionMeta{{
		Ref: model.SessionRef{Agent: model.AgentCodex, ID: "c1"},
		CWD: "/home/user/project1",
	}}})

	counts, err := svc.AgentCounts(FilterOpts{Agent: string(model.AgentCodex)})
	if err != nil {
		t.Fatalf("AgentCounts: %v", err)
	}
	// s3 is archived and excluded by default.
	if counts[string(model.AgentClaude)] != 2 || counts[string(model.AgentCodex)] != 1 {
		t.Errorf("counts = %v, want claude-code:2 codex:1", counts)
	}

	counts, err = svc.AgentCounts(FilterOpts{Archived: true, LiveOnly: true})
	if err != nil {
		t.Fatalf("AgentCounts live: %v", err)
	}
	if counts[string(model.AgentClaude)] != 1 || counts[string(model.AgentCodex)] != 0 {
		t.Errorf("live counts = %v, want claude-code:1", counts)
	}
}

func TestGetSessionMeta(t *testing.T) {
	svc, _ := setupTestService(t)

	meta, err := svc.GetSessionMeta(model.SessionRef{Agent: model.AgentClaude, ID: "s1"})
	if err != nil {
		t.Fatalf("GetSessionMeta: %v", err)
	}
	if meta.Title != "First Session" {
		t.Errorf("title = %q, want 'First Session'", meta.Title)
	}

	_, err = svc.GetSessionMeta(model.SessionRef{Agent: model.AgentClaude, ID: "nonexistent"})
	if !errors.Is(err, ErrUnknownSession) {
		t.Errorf("expected ErrUnknownSession, got %v", err)
	}
}

func TestGetMessages_PagingAndLRU(t *testing.T) {
	svc, fake := setupTestService(t)
	ctx := context.Background()
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}

	// Page 1: offset 0, limit 2
	page1, err := svc.GetMessages(ctx, ref, 0, 2)
	if err != nil {
		t.Fatalf("GetMessages page 1: %v", err)
	}
	if len(page1.Messages) != 2 || page1.TotalCount != 3 || !page1.HasMore {
		t.Errorf("page 1 = %+v, want 2 messages, total 3, hasMore true", page1)
	}

	// Page 2: offset 2, limit 2
	page2, err := svc.GetMessages(ctx, ref, 2, 2)
	if err != nil {
		t.Fatalf("GetMessages page 2: %v", err)
	}
	if len(page2.Messages) != 1 || page2.TotalCount != 3 || page2.HasMore {
		t.Errorf("page 2 = %+v, want 1 message, total 3, hasMore false", page2)
	}

	// Verify LRU cache hit by clearing the provider's transcript
	delete(fake.Transcripts, "s1")
	pageCached, err := svc.GetMessages(ctx, ref, 0, 1)
	if err != nil {
		t.Fatalf("GetMessages cached: %v", err)
	}
	if len(pageCached.Messages) != 1 {
		t.Errorf("expected cached messages, got %d", len(pageCached.Messages))
	}

	// Invalid pagination bounds
	if _, err := svc.GetMessages(ctx, ref, -1, 10); err == nil {
		t.Error("expected error for negative offset")
	}
	if _, err := svc.GetMessages(ctx, ref, 0, 0); err == nil {
		t.Error("expected error for zero limit")
	}
}

func TestGetBlob(t *testing.T) {
	svc, _ := setupTestService(t)
	ctx := context.Background()
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}

	// Text blob
	textBlob, err := svc.GetBlob(ctx, ref, "text-blob")
	if err != nil {
		t.Fatalf("GetBlob text: %v", err)
	}
	if textBlob.IsBinary || textBlob.Data != "Plain text blob content" || textBlob.Mime != "text/plain" {
		t.Errorf("unexpected text blob: %+v", textBlob)
	}

	// Binary blob
	binBlob, err := svc.GetBlob(ctx, ref, "binary-blob")
	if err != nil {
		t.Fatalf("GetBlob binary: %v", err)
	}
	if !binBlob.IsBinary || binBlob.Mime != "application/octet-stream" {
		t.Errorf("unexpected binary blob: %+v", binBlob)
	}

	// Unknown provider
	_, err = svc.GetBlob(ctx, model.SessionRef{Agent: "unknown", ID: "s1"}, "blob")
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("expected ErrUnknownProvider, got %v", err)
	}
}

func TestCopyResumeCommand(t *testing.T) {
	svc, _ := setupTestService(t)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}

	cmd, err := svc.CopyResumeCommand(ref)
	if err != nil {
		t.Fatalf("CopyResumeCommand: %v", err)
	}
	if cmd == "" {
		t.Fatal("empty resume command")
	}

	_, err = svc.CopyResumeCommand(model.SessionRef{Agent: model.AgentClaude, ID: "missing"})
	if !errors.Is(err, ErrUnknownSession) {
		t.Errorf("expected ErrUnknownSession, got %v", err)
	}
}

func TestRevealSource(t *testing.T) {
	svc, _ := setupTestService(t)
	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}

	dir, err := svc.RevealSource(ref)
	if err != nil {
		t.Fatalf("RevealSource: %v", err)
	}
	expected := filepath.Clean("/home/user/project1/.sessions")
	if dir != expected {
		t.Errorf("dir = %q, want %q", dir, expected)
	}
}

func TestShellEscaping(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "''"},
		{"simple", "simple"},
		{"path/to/file.txt", "path/to/file.txt"},
		{"has space", "'has space'"},
		{"has'quote", `'has'"'"'quote'`},
		{"$dollar", "'$dollar'"},
	}
	for _, tc := range tests {
		got := shellEscape(tc.input)
		if got != tc.want {
			t.Errorf("shellEscape(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestDiagnostics(t *testing.T) {
	svc, _ := setupTestService(t)
	var d provider.Diagnostics
	d.Warn("/path", 1, "test warning")
	svc.ApplyReport(scan.Report{
		Diag:   map[model.AgentID]provider.Diagnostics{model.AgentClaude: d},
		Errors: map[model.AgentID]error{model.AgentCodex: errors.New("scan failed")},
	})

	total, err := svc.Diagnostics()
	if err != nil {
		t.Fatalf("Diagnostics: %v", err)
	}
	if len(total.Warnings) != 1 {
		t.Errorf("warnings count = %d, want 1", len(total.Warnings))
	}
	errs := svc.ScanErrors()
	if errs["codex"] != "scan failed" {
		t.Errorf("codex scan error = %q, want 'scan failed'", errs["codex"])
	}
}

func TestApp_Delegation(t *testing.T) {
	svc, _ := setupTestService(t)
	app := NewAppWithService(svc)
	app.OnStartup(context.Background())

	groups, err := app.ListGroups(GroupModeDirAgent, FilterOpts{})
	if err != nil {
		t.Fatalf("App.ListGroups: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("expected groups from App.ListGroups")
	}

	sessions, err := app.ListSessions("", FilterOpts{}, SortOpts{})
	if err != nil {
		t.Fatalf("App.ListSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions count = %d, want 2", len(sessions))
	}

	ref := model.SessionRef{Agent: model.AgentClaude, ID: "s1"}
	meta, err := app.GetSessionMeta(ref)
	if err != nil {
		t.Fatalf("App.GetSessionMeta: %v", err)
	}
	if meta.Title != "First Session" {
		t.Errorf("meta.Title = %q", meta.Title)
	}

	msgs, err := app.GetMessages(ref, 0, 10)
	if err != nil {
		t.Fatalf("App.GetMessages: %v", err)
	}
	if len(msgs.Messages) != 3 {
		t.Errorf("messages count = %d, want 3", len(msgs.Messages))
	}

	blob, err := app.GetBlob(ref, "text-blob")
	if err != nil {
		t.Fatalf("App.GetBlob: %v", err)
	}
	if blob.Data != "Plain text blob content" {
		t.Errorf("blob.Data = %q", blob.Data)
	}

	cmd, err := app.CopyResumeCommand(ref)
	if err != nil {
		t.Fatalf("App.CopyResumeCommand: %v", err)
	}
	if cmd == "" {
		t.Fatal("empty command from App.CopyResumeCommand")
	}
}
