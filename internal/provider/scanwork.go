package provider

import (
	"context"
	"sync/atomic"
)

// ScanWork is opt-in diagnostic accounting for synthetic scan benchmarks. Counters
// describe logical scanner work, not OS reads, filesystem cache misses or bytes
// fetched by discovery/index/sidecar inspection. It retains no paths or contents.
// A collector must not be reset while scans using it are running.
type ScanWork struct {
	SourceOpens       atomic.Int64
	Records           atomic.Int64
	RecordBytes       atomic.Int64
	CheckpointDecodes atomic.Int64
	CheckpointEncodes atomic.Int64
}

type scanWorkKey struct{}

// WithScanWork attaches a concurrency-safe collector to this scan only. Ordinary
// scans have no collector; there is no global hook or persisted schema change.
func WithScanWork(ctx context.Context, work *ScanWork) context.Context {
	return context.WithValue(ctx, scanWorkKey{}, work)
}

// ScanWorkFromContext returns the optional collector for provider scan internals.
func ScanWorkFromContext(ctx context.Context) *ScanWork {
	work, _ := ctx.Value(scanWorkKey{}).(*ScanWork)
	return work
}
