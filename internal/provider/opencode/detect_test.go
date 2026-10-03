package opencode

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ginkcode/agent-sessions/internal/testutil/platform"
)

// fixtureDB creates a temporary SQLite store with no user or authentication
// data. The real OpenCode store is never opened by these tests.
func fixtureDB(t *testing.T, root string, tables ...string) {
	t.Helper()
	path := filepath.Join(root, dbName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		// Table names here are constants from test cases below, never user input.
		if _, err := db.Exec(`CREATE TABLE "` + table + `" (id TEXT)`); err != nil {
			_ = db.Close()
			t.Fatalf("create fixture table %s: %v", table, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func fixtureLegacy(t *testing.T, root string) {
	t.Helper()
	project := filepath.Join(root, "storage", legacySession, "project id")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "session id.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectAbsentRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	d, err := New(root, nil).Detect(t.Context())
	if err != nil {
		t.Fatalf("absent root: %v", err)
	}
	if d.Present || len(d.Generations) != 0 || !reflect.DeepEqual(d.Roots, []string{root}) {
		t.Errorf("absent root detection = %+v", d)
	}
}

func TestDetectGenerations(t *testing.T) {
	cases := []struct {
		name, tables string
		legacy       bool
		want         []string
		warn         bool
	}{
		{name: "empty root"},
		{name: "v2", tables: "session_v2 session_message", want: []string{GenV2}},
		{name: "v1", tables: "session message part", want: []string{GenV1}},
		{name: "v1 without part", tables: "session message", want: []string{GenV1}, warn: true},
		{name: "v2 and v1", tables: "session_v2 session_message session message part", want: []string{GenV2, GenV1}},
		{name: "legacy only", legacy: true, want: []string{GenLegacy}},
		{name: "v2 and legacy", tables: "session_v2 session_message", legacy: true, want: []string{GenV2, GenLegacy}},
		{name: "v1 and legacy", tables: "session message part", legacy: true, want: []string{GenV1, GenLegacy}},
		{name: "all generations", tables: "session_v2 session_message session message part", legacy: true, want: []string{GenV2, GenV1, GenLegacy}},
		{name: "partial v2", tables: "session_v2", warn: true},
		{name: "partial v1", tables: "message part", warn: true},
		{name: "two partial schemas", tables: "session_message session part", warn: true},
		{name: "partial db and legacy", tables: "session_v2", legacy: true, want: []string{GenLegacy}, warn: true},
		{name: "unrelated schema", tables: "something_else"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.tables != "" {
				fixtureDB(t, root, strings.Fields(tc.tables)...)
			}
			if tc.legacy {
				fixtureLegacy(t, root)
			}
			d, err := New(root, nil).Detect(t.Context())
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if d.Present != (len(tc.want) != 0) || !reflect.DeepEqual(d.Roots, []string{root}) || !reflect.DeepEqual(d.Generations, tc.want) {
				t.Errorf("Detect = %+v; want Present=%v, Roots=[%s], Generations=%v", d, len(tc.want) != 0, root, tc.want)
			}
			if (len(d.Notes) != 0) != tc.warn {
				t.Errorf("Notes = %v; want warning=%v", d.Notes, tc.warn)
			}
		})
	}
}

func TestDetectEmptyLegacyDirectoriesDoNotCount(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		filepath.Join(root, "storage", legacySession, "empty-project"),
		filepath.Join(root, "storage", "session_diff", "project"),
		filepath.Join(root, "storage", legacyMessage, "session"),
		filepath.Join(root, "storage", legacyPart, "message"),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "storage", legacySession, "empty-project", "not-json.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "storage", legacyMessage, "session", "message.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := New(root, nil).Detect(t.Context())
	if err != nil || d.Present {
		t.Fatalf("Detect = %+v, %v; want not present", d, err)
	}
	fixtureLegacy(t, root)
	d, err = New(root, nil).Detect(t.Context())
	if err != nil || !reflect.DeepEqual(d.Generations, []string{GenLegacy}) {
		t.Fatalf("Detect with a legacy session = %+v, %v", d, err)
	}
}

func TestDetectRootAndDBWithSpaces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "open code data")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixtureDB(t, root, "session_v2", "session_message")
	fixtureLegacy(t, root)
	d, err := New(root, nil).Detect(t.Context())
	if err != nil || !reflect.DeepEqual(d.Generations, []string{GenV2, GenLegacy}) {
		t.Fatalf("Detect path with spaces = %+v, %v", d, err)
	}
}

func TestDetectUnreadableOrCorruptDatabase(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, dbName)
	if err := os.WriteFile(path, []byte("not a SQLite database"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureLegacy(t, root)
	// The error quotes the path, which escapes Windows backslashes.
	quoted := strconv.Quote(path)
	_, err := New(root, nil).Detect(t.Context())
	if err == nil || !strings.Contains(err.Error(), quoted) {
		t.Fatalf("corrupt db error = %v; want contextual error naming %s", err, path)
	}
	if os.Geteuid() == 0 || !platform.ModeBits {
		return // root bypasses file permissions; Windows has none to drop
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	_, err = New(root, nil).Detect(t.Context())
	if err == nil || !strings.Contains(err.Error(), quoted) {
		t.Fatalf("unreadable db error = %v; want contextual error naming %s", err, path)
	}
}

func TestDetectContextError(t *testing.T) {
	root := t.TempDir()
	fixtureDB(t, root, "session_v2", "session_message")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := New(root, nil).Detect(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Detect canceled: %v; want context.Canceled", err)
	}
}

func TestDetectNeverOpensAuthentication(t *testing.T) {
	root := t.TempDir()
	fixtureDB(t, root, "session_v2", "session_message", "credential", "account", "control_account")
	if err := os.WriteFile(filepath.Join(root, "auth.json"), []byte("not valid JSON"), 0); err != nil {
		t.Fatal(err)
	}
	d, err := New(root, nil).Detect(t.Context())
	if err != nil || !reflect.DeepEqual(d.Generations, []string{GenV2}) {
		t.Fatalf("Detect with unreadable auth = %+v, %v", d, err)
	}
	// The file remains inaccessible and untouched after detection.
	authPath := filepath.Join(root, "auth.json")
	if err := os.Chmod(authPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(authPath); err != nil || string(got) != "not valid JSON" {
		t.Errorf("auth.json changed: %q, %v", got, err)
	}
}
