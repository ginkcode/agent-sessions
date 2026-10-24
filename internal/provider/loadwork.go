package provider

import (
	"context"
	"io"
	"os"
	"sync/atomic"

	"github.com/ginkcode/agent-sessions/internal/jsonl"
)

// LoadWork is optional synthetic-load accounting. SourceBytes counts bytes
// delivered by file Read calls to transcript readers (including incomplete tails),
// not physical disk I/O. Resolution/scanning is separate: use WithScanWork too.
// It retains no paths or content. Do not reset a collector during a load.
type LoadWork struct {
	Calls       atomic.Int64
	SourceOpens atomic.Int64
	SourceBytes atomic.Int64
}
type loadWorkKey struct{}

// WithLoadWork attaches a concurrency-safe collector to loads using ctx only.
func WithLoadWork(ctx context.Context, work *LoadWork) context.Context {
	return context.WithValue(ctx, loadWorkKey{}, work)
}

// LoadWorkFromContext returns the optional load collector, or nil.
func LoadWorkFromContext(ctx context.Context) *LoadWork {
	work, _ := ctx.Value(loadWorkKey{}).(*LoadWork)
	return work
}

type observedLoadReader struct {
	io.Reader
	work *LoadWork
}

func (r observedLoadReader) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	r.work.SourceBytes.Add(int64(n))
	return n, err
}

// OpenLoadSource opens a transcript reader with opt-in byte accounting. The
// caller closes the returned Closer; without a collector this is jsonl.Open.
func OpenLoadSource(ctx context.Context, path string) (*jsonl.Reader, io.Closer, error) {
	work := LoadWorkFromContext(ctx)
	if work == nil {
		r, err := jsonl.Open(path, 0)
		return r, r, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	work.SourceOpens.Add(1)
	return jsonl.NewReader(observedLoadReader{f, work}, 0), f, nil
}
