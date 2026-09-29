//go:build darwin

package manage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFinderTrashPassesPathOutsideScript(t *testing.T) {
	var got []string
	f := FinderTrash{Exec: func(_ context.Context, argv []string) error {
		got = append([]string(nil), argv...)
		return nil
	}}
	path := `/tmp/session "quoted".jsonl`
	if err := f.Trash(path); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0] != "osascript" || got[1] != "-e" || got[3] != path {
		t.Fatalf("path must be a separate argument, got %#v", got)
	}
	if strings.Contains(got[2], path) {
		t.Fatalf("path interpolated into script: %s", got[2])
	}
}

func TestFinderTrashHidesCommandFailure(t *testing.T) {
	f := FinderTrash{Exec: func(context.Context, []string) error {
		return errors.New("osascript: permission denied: /secret/session.jsonl")
	}}
	err := f.Trash("/tmp/session.jsonl")
	if err == nil || err.Error() != "trash operation failed" {
		t.Fatalf("unexpected error: %v", err)
	}
}
