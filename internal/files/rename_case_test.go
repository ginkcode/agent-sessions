//go:build darwin || windows

package files

import (
	"context"
	"os"
	"testing"
)

func TestRenameCaseOnly(t *testing.T) {
	root := t.TempDir()
	renameFixture(t, root, "lower.txt")
	if err := Rename(context.Background(), EditPolicy{}, root, "lower.txt", "LOWER.txt"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "LOWER.txt" {
		t.Fatalf("case spelling not changed: %v", entries)
	}
}
