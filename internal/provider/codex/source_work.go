package codex

import (
	"context"
	"sync/atomic"
)

// SourceWork accounts for optional proof reconciliation, separately from parsed
// ScanWork records. Bytes counts all digest reads, including failed proofs.
// Enumerations counts discovery passes (not individual directory syscalls).
type SourceWork struct {
	Passes       atomic.Int64
	Enumerations atomic.Int64
	IndexReads   atomic.Int64
	Opens        atomic.Int64
	Bytes        atomic.Int64
}
type sourceWorkKey struct{}

func WithSourceWork(ctx context.Context, work *SourceWork) context.Context {
	return context.WithValue(ctx, sourceWorkKey{}, work)
}

func sourceWork(ctx context.Context) *SourceWork {
	work, _ := ctx.Value(sourceWorkKey{}).(*SourceWork)
	return work
}
