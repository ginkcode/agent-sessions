package index

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/pathutil"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// TestStartupLargeIndexOpen times the warm writer's open steps on a generated
// index of AS_LARGE_INDEX_MB (default 300) megabytes, mostly FTS text, so the
// cost of each step before catalog publication can be compared on a cache of
// realistic size. Opt-in: generation takes minutes.
func TestStartupLargeIndexOpen(t *testing.T) {
	if os.Getenv("AS_STARTUP_BASELINE") != "1" {
		t.Skip("set AS_STARTUP_BASELINE=1")
	}
	targetMB := 300
	if v := os.Getenv("AS_LARGE_INDEX_MB"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			t.Fatalf("AS_LARGE_INDEX_MB=%q", v)
		}
		targetMB = n
	}
	ctx := context.Background()
	dir := t.TempDir()
	const key = "large"

	genStart := time.Now()
	sessions := generateLargeIndex(t, ctx, dir, key, int64(targetMB)<<20)
	dbPath := filepath.Join(dir, FileName(SchemaVersion(), key))
	t.Logf("generated %d sessions in %v: db %s, wal %s", sessions, time.Since(genStart).Round(time.Millisecond),
		fileMB(dbPath), fileMB(dbPath+"-wal"))

	const runs = 5
	var open, ping, check, migrate, catalog, hints []time.Duration
	for range runs {
		start := time.Now()
		reader, err := OpenReadOnly(ctx, dir, key)
		if err != nil {
			t.Fatal(err)
		}
		cached, err := reader.LoadCatalog(ctx)
		if err != nil || len(cached) != sessions {
			t.Fatalf("hints rows=%d err=%v", len(cached), err)
		}
		_ = reader.Close()
		hints = append(hints, time.Since(start))
		// The whole verified path as the writer runs it.
		start = time.Now()
		db, err := Open(ctx, dir, key)
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, time.Since(start))
		start = time.Now()
		metas, err := db.LoadCatalog(ctx)
		if err != nil {
			t.Fatal(err)
		}
		catalog = append(catalog, time.Since(start))
		if len(metas) != sessions {
			t.Fatalf("catalog has %d sessions, want %d", len(metas), sessions)
		}
		_ = db.Close()

		// The steps inside Open, on a handle opened the same way.
		params := url.Values{}
		for _, p := range []string{"journal_mode(WAL)", "busy_timeout(5000)", "foreign_keys(ON)", "secure_delete(ON)"} {
			params.Add("_pragma", p)
		}
		sqlDB, err := sql.Open("sqlite", pathutil.SQLiteURI(dbPath, params))
		if err != nil {
			t.Fatal(err)
		}
		start = time.Now()
		if err := sqlDB.PingContext(ctx); err != nil {
			t.Fatal(err)
		}
		ping = append(ping, time.Since(start))
		start = time.Now()
		if err := checkIntegrity(ctx, sqlDB); err != nil {
			t.Fatal(err)
		}
		check = append(check, time.Since(start))
		migrations, err := loadMigrations()
		if err != nil {
			t.Fatal(err)
		}
		start = time.Now()
		if err := runMigrations(ctx, sqlDB, migrations); err != nil {
			t.Fatal(err)
		}
		// Includes its own integrity check; do not subtract independently
		// measured timings, which can differ due to page-cache effects.
		migrate = append(migrate, time.Since(start))
		_ = sqlDB.Close()
	}
	t.Logf("warm page cache, %d runs (median / max):", runs)
	for _, s := range []struct {
		name string
		d    []time.Duration
	}{
		{"read-only catalog hints", hints},
		{"index.Open total", open},
		{"  ping", ping},
		{"  quick_check(1)", check},
		{"  migrations incl check", migrate},
		{"LoadCatalog", catalog},
	} {
		slices.Sort(s.d)
		t.Logf("  %-24s %10v %10v", s.name, s.d[len(s.d)/2].Round(time.Microsecond), s.d[len(s.d)-1].Round(time.Microsecond))
	}
}

// generateLargeIndex commits sessions with FTS documents until the database
// reaches target bytes after checkpointing. One session has 3,000 messages; the rest
// have a few hundred. Text is pseudo-random words from a fixed vocabulary.
func generateLargeIndex(t *testing.T, ctx context.Context, dir, key string, target int64) int {
	t.Helper()
	db, err := Open(ctx, dir, key)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rng := rand.New(rand.NewPCG(1, 2))
	vocab := make([]string, 30000)
	for i := range vocab {
		n := 3 + rng.IntN(8)
		b := make([]byte, n)
		for j := range b {
			b[j] = byte('a' + rng.IntN(26))
		}
		vocab[i] = string(b)
	}
	text := func(words int) string {
		var sb strings.Builder
		for i := range words {
			if i > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(vocab[rng.IntN(len(vocab))])
		}
		return sb.String()
	}
	size := func() int64 {
		var n int64
		for _, p := range []string{db.Path(), db.Path() + "-wal"} {
			if fi, err := os.Stat(p); err == nil {
				n += fi.Size()
			}
		}
		return n
	}

	now := time.Now().UTC()
	sessions := 0
	for {
		// Check the checkpointed database, not DB+WAL: the latter can count
		// the same pages twice and stop well below the requested size.
		if _, err := db.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			t.Fatal(err)
		}
		if size() >= target {
			break
		}
		messages := 200 + rng.IntN(400)
		if sessions == 0 {
			messages = 3000
		}
		m := model.SessionMeta{
			Ref:         model.SessionRef{Agent: model.AgentClaude, ID: fmt.Sprintf("large-%04d", sessions)},
			Title:       text(6),
			SourcePath:  fmt.Sprintf("/generated/projects/p%02d/large-%04d.jsonl", sessions%20, sessions),
			CWD:         fmt.Sprintf("/generated/work/p%02d", sessions%20),
			CreatedAt:   now.Add(-time.Duration(sessions) * time.Hour),
			UpdatedAt:   now.Add(-time.Duration(sessions) * time.Minute),
			Counts:      model.MessageCounts{User: messages / 2, Assistant: messages - messages/2},
			Model:       "generated-model",
			FirstPrompt: text(20),
		}
		if err := db.CommitScan(ctx, model.AgentClaude, provider.ScanResult{
			Changed: []model.SessionMeta{m},
			State:   provider.ScanState{Cursor: strconv.Itoa(sessions)},
		}); err != nil {
			t.Fatal(err)
		}
		tr := &model.Transcript{Meta: m, Messages: make([]model.Message, messages)}
		for i := range messages {
			// Mostly short turns, some long tool output.
			words := 40 + rng.IntN(120)
			if rng.IntN(10) == 0 {
				words = 600 + rng.IntN(1200)
			}
			tr.Messages[i] = model.Message{
				Role:  model.RoleUser,
				Parts: []model.Part{{Kind: model.PartText, Text: text(words)}},
			}
		}
		if err := db.replaceFTSDocs(ctx, ftsJob{ref: m.Ref.Key(), revision: 1}, tr); err != nil {
			t.Fatal(err)
		}
		sessions++
	}
	return sessions
}

func fileMB(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "none"
	}
	return fmt.Sprintf("%.1f MB", float64(fi.Size())/(1<<20))
}
