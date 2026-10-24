package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// Keep explicit stale-dispatch coverage alongside the production accepting
// implementation and its content-proof tests in source_test.go.
type staleSourceProvider struct{ *Provider }

func (p staleSourceProvider) LoadFromSource(context.Context, model.SessionRef, model.SessionMeta) (*model.Transcript, error) {
	return nil, provider.ErrStaleSource
}

func (p staleSourceProvider) BlobFromSource(context.Context, model.SessionRef, model.SessionMeta, string) ([]byte, error) {
	return nil, provider.ErrStaleSource
}

func TestSourceContractCanonicalFallback(t *testing.T) {
	p, ref, path := syntheticRollout(t,
		record("session_meta", `{"id":"fixture"}`),
		record("response_item", `{"type":"message","role":"user","content":[{"type":"input_file","file_data":"data:text/plain;base64,SEVMTE8="}]}`),
	)
	ctx := context.Background()
	want, err := p.Load(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	key := want.Messages[0].Parts[0].File.Ref
	scan, err := p.Scan(ctx, provider.ScanState{})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "rollout-outside.jsonl")
	if err := os.WriteFile(outside, []byte(record("session_meta", `{"id":"fixture"}`)), 0o600); err != nil {
		t.Fatal(err)
	}
	duplicate := filepath.Join(p.root, "archived_sessions", "rollout-duplicate.jsonl")
	if err := os.MkdirAll(filepath.Dir(duplicate), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(duplicate, mustRead(t, path), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, hintPath := range []string{outside, duplicate, filepath.Join(p.root, "missing.jsonl"), "../../rollout-outside.jsonl"} {
		t.Run(hintPath, func(t *testing.T) {
			hint := scan.Changed[0]
			hint.SourcePath = hintPath
			hint.Title = "forged overlay"
			// Both unsupported providers and explicit stale rejection must resolve
			// canonically. Neither path opens a caller-supplied source.
			for _, impl := range []provider.Provider{p, staleSourceProvider{p}} {
				got, err := provider.LoadWithSource(ctx, impl, ref, hint)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("Load = %+v, %v", got, err)
				}
				data, err := provider.BlobWithSource(ctx, impl, ref, hint, key)
				if err != nil || string(data) != "HELLO" {
					t.Fatalf("Blob = %q, %v", data, err)
				}
				if _, err := provider.BlobWithSource(ctx, impl, ref, hint, "file:"+outside); err == nil {
					t.Fatal("hint bypassed blob key validation")
				}
			}
		})
	}
	// A stale hint cannot rescue a canonical source that has become an escape.
	if err := os.Remove(duplicate); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, impl := range []provider.Provider{p, staleSourceProvider{p}} {
		if _, err := provider.LoadWithSource(ctx, impl, ref, scan.Changed[0]); err == nil {
			t.Fatal("symlink escape loaded")
		}
		if _, err := provider.BlobWithSource(ctx, impl, ref, scan.Changed[0], key); err == nil {
			t.Fatal("symlink escape blob loaded")
		}
	}
}
