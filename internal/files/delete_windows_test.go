//go:build windows

package files

import (
	"context"
	"testing"
)

func TestDeleteRejectsWindowsAliases(t *testing.T) {
	root := t.TempDir()
	deleteFixture(t, root, "folder/file")
	for _, rel := range []string{"folder/file:stream", "folder/file.", "folder/file ", "folder./file", "folder/auth.json.", "folder/.. /file", "GIT~1", "folder/AUTH~1.JSO"} {
		if err := Delete(context.Background(), EditPolicy{}, root, rel); err == nil {
			t.Fatalf("accepted alias %q", rel)
		}
	}
}
