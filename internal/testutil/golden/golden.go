// Package golden provides golden-file helpers for tests. Importing this
// package registers a boolean -update test flag; call golden.JSON and run
// make golden (go test -update) to rewrite the expected files after an
// intentional change.
package golden

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
)

// update rewrites golden files instead of comparing against them. Enable
// with go test -update.
var update = flag.Bool("update", false, "rewrite golden files")

// JSON indent-marshals got and compares it to testdata/golden/<name>.json
// relative to the calling test's directory. With -update the file is
// rewritten instead of compared. On mismatch the test fails with a unified
// line diff between expected and actual.
func JSON(t *testing.T, name string, got any) {
	t.Helper()

	path := filepath.Join("testdata", "golden", name+".json")

	gotJSON, err := Marshal(got)
	if err != nil {
		t.Fatalf("golden: marshal %s: %v", name, err)
	}

	if *update {
		if err := write(path, gotJSON); err != nil {
			t.Fatalf("golden: write %s: %v", path, err)
		}
		return
	}

	wantJSON, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden: read %s: %v (run go test -update to create it)", path, err)
	}

	if bytes.Equal(wantJSON, gotJSON) {
		return
	}
	t.Errorf("golden mismatch for %s:\n%s", path, diff(string(wantJSON), string(gotJSON)))
}

// Text compares got against testdata/golden/<filename> relative to the calling
// test's directory. With -update the file is rewritten instead of compared.
func Text(t *testing.T, filename string, got string) {
	t.Helper()

	path := filepath.Join("testdata", "golden", filename)

	if *update {
		if err := write(path, []byte(got)); err != nil {
			t.Fatalf("golden: write %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden: read %s: %v (run go test -update to create it)", path, err)
	}

	if string(want) == got {
		return
	}
	t.Errorf("golden mismatch for %s:\n%s", path, diff(string(want), got))
}

// Marshal indent-marshals v in a canonical form: object keys sorted, HTML
// escaping off, and a trailing newline.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// write stores data, creating parent directories as needed, so that a first
// -update run on a fresh checkout succeeds.
func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// diff renders a unified diff between want and got. It uses gotextdiff.
func diff(want, got string) string {
	edits := myers.ComputeEdits(span.URIFromPath("want"), want, got)
	if len(edits) == 0 {
		return ""
	}
	return fmt.Sprint(gotextdiff.ToUnified("want", "got", want, edits))
}
