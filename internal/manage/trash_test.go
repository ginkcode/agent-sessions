package manage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGioTrashHidesCommandFailure(t *testing.T) {
	g := GioTrash{Exec: func(context.Context, []string) error {
		return errors.New("gio: permission denied: /secret/session.jsonl")
	}}
	err := g.Trash("/tmp/session.jsonl")
	if err == nil || !strings.Contains(err.Error(), "gio trash") {
		t.Fatalf("unexpected error: %v", err)
	}
}
