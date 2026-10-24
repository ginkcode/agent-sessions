package provider_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
	"github.com/ginkcode/agent-sessions/internal/provider/providertest"
)

type sourceFake struct {
	*providertest.Fake
	err                                    error
	cancel                                 context.CancelFunc
	loads, blobs, sourceLoads, sourceBlobs int
}

func (f *sourceFake) Load(ctx context.Context, ref model.SessionRef) (*model.Transcript, error) {
	f.loads++
	return f.Fake.Load(ctx, ref)
}

func (f *sourceFake) Blob(ctx context.Context, ref model.SessionRef, key string) ([]byte, error) {
	f.blobs++
	return f.Fake.Blob(ctx, ref, key)
}

func (f *sourceFake) LoadFromSource(_ context.Context, _ model.SessionRef, source model.SessionMeta) (*model.Transcript, error) {
	f.sourceLoads++
	if f.cancel != nil {
		f.cancel()
	}
	return &model.Transcript{Meta: source}, f.err
}

func (f *sourceFake) BlobFromSource(_ context.Context, _ model.SessionRef, _ model.SessionMeta, _ string) ([]byte, error) {
	f.sourceBlobs++
	if f.cancel != nil {
		f.cancel()
	}
	return []byte("hint"), f.err
}

func TestSourceDispatchAndFallback(t *testing.T) {
	ref := model.SessionRef{Agent: model.AgentCodex, ID: "fixture"}
	failure := errors.New("read failure")
	for _, tc := range []struct {
		name                 string
		err                  error
		mismatch, cancel     bool
		wantSource, wantBase int
		wantErr              error
	}{
		{name: "validated", wantSource: 1},
		{name: "wrapped stale", err: fmt.Errorf("changed: %w", provider.ErrStaleSource), wantSource: 1, wantBase: 1},
		{name: "other error", err: failure, wantSource: 1, wantErr: failure},
		{name: "mismatched identity", mismatch: true, wantBase: 1},
		{name: "canceled stale", err: provider.ErrStaleSource, cancel: true, wantSource: 1, wantErr: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, blob := range []bool{false, true} {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				f := &sourceFake{Fake: providertest.NewFake(ref.Agent, "fixture"), err: tc.err}
				canonical := model.SessionMeta{Ref: ref, Title: "canonical"}
				f.Transcripts[ref.ID] = &model.Transcript{Meta: canonical}
				f.Blobs[ref.Key()+":key"] = []byte("canonical")
				source := model.SessionMeta{Ref: ref, Title: "hint"}
				if tc.mismatch {
					source.Ref.ID = "other"
				}
				if tc.cancel {
					f.cancel = cancel
				}
				var err error
				if blob {
					var data []byte
					data, err = provider.BlobWithSource(ctx, f, ref, source, "key")
					if tc.wantBase == 1 && string(data) != "canonical" {
						t.Fatalf("fallback bytes = %q", data)
					}
				} else {
					var tr *model.Transcript
					tr, err = provider.LoadWithSource(ctx, f, ref, source)
					if tc.wantBase == 1 && (tr == nil || tr.Meta != canonical) {
						t.Fatalf("fallback transcript = %+v", tr)
					}
				}
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				if f.loads+f.blobs != tc.wantBase || f.sourceLoads+f.sourceBlobs != tc.wantSource {
					t.Fatalf("base/source calls = %d/%d, want %d/%d", f.loads+f.blobs, f.sourceLoads+f.sourceBlobs, tc.wantBase, tc.wantSource)
				}
			}
		})
	}
}

func TestSourceCanceledBeforeDispatch(t *testing.T) {
	f := &sourceFake{Fake: providertest.NewFake(model.AgentCodex, "fixture")}
	ref := model.SessionRef{Agent: model.AgentCodex, ID: "fixture"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.LoadWithSource(ctx, f, ref, model.SessionMeta{Ref: ref}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := provider.BlobWithSource(ctx, f, ref, model.SessionMeta{Ref: ref}, "key"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if f.loads+f.blobs+f.sourceLoads+f.sourceBlobs != 0 {
		t.Fatal("canceled call dispatched")
	}
}
