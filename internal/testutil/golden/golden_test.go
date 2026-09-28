package golden

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sampleStruct struct {
	Name  string   `json:"name"`
	Score int      `json:"score"`
	Tags  []string `json:"tags,omitempty"`
}

func TestGolden_MatchesExisting(t *testing.T) {
	val := sampleStruct{
		Name:  "session-1",
		Score: 42,
		Tags:  []string{"claude", "test"},
	}
	JSON(t, "sample", val)
}

func TestGolden_UpdateMode(t *testing.T) {
	orig := *update
	t.Cleanup(func() { *update = orig })

	tmpDir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	*update = true
	dummyT := &testing.T{}
	val := map[string]any{"written": true, "count": 10}
	JSON(dummyT, "dynamic", val)
	if dummyT.Failed() {
		t.Fatalf("expected JSON with update=true to succeed")
	}

	goldenPath := filepath.Join(tmpDir, "testdata", "golden", "dynamic.json")
	content, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("expected golden file to be created: %v", err)
	}
	if !strings.Contains(string(content), `"written": true`) {
		t.Errorf("expected content to contain written: true, got %s", content)
	}

	// Now switch update back to false and verify it matches
	*update = false
	JSON(t, "dynamic", val)
}

func TestDiff(t *testing.T) {
	got := diff("one\ntwo\nthree\n", "one\nnew\nthree\n")
	for _, part := range []string{"--- want", "+++ got", "-two", "+new"} {
		if !strings.Contains(got, part) {
			t.Errorf("diff missing %q:\n%s", part, got)
		}
	}
}

func TestMarshal(t *testing.T) {
	got, err := Marshal(map[string]any{"z": "<hello>", "a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{\n  \"a\": 1,\n  \"z\": \"<hello>\"\n}\n" {
		t.Errorf("unexpected marshaling: %q", got)
	}
}
