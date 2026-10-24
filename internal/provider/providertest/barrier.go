package providertest

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/ginkcode/agent-sessions/internal/model"
	"github.com/ginkcode/agent-sessions/internal/provider"
)

// LoadAttempt is one blocked load. Tests receive Started, then release individual
// attempts in any order with Reply or Forward, at most once per attempt. The
// reply is buffered, so releasing an already-cancelled attempt never blocks.
// Context is the actual provider context, allowing cancellation ownership checks.
type LoadAttempt struct {
	Ref     model.SessionRef
	Context context.Context
	reply   chan LoadReply
}

// LoadReply is the result a test supplies for one LoadAttempt.
type LoadReply struct {
	Transcript *model.Transcript
	Err        error
	forward    bool
}

// Reply completes the attempt with tr and err without calling the wrapped provider.
func (a *LoadAttempt) Reply(tr *model.Transcript, err error) {
	a.reply <- LoadReply{Transcript: tr, Err: err}
}

// Forward releases the attempt to the embedded provider using the attempt context.
func (a *LoadAttempt) Forward() { a.reply <- LoadReply{forward: true} }

// Barrier embeds a provider for non-load methods. Each Load announces itself and
// waits for an explicit reply or cancellation; there are no sleep-based gates.
// Do not change IgnoreCancellation while loads run. Closing Stop releases all
// blocked loads, including attempts waiting to announce themselves.
type Barrier struct {
	provider.Provider
	Started            chan *LoadAttempt
	Stop               chan struct{}
	IgnoreCancellation bool
	Calls              atomic.Int64
	once               sync.Once
}

// NewBarrier wraps p. capacity bounds how many announced attempts may be queued
// before a Load waits for the test to receive from Started.
func NewBarrier(p provider.Provider, capacity int) *Barrier {
	return &Barrier{Provider: p, Started: make(chan *LoadAttempt, capacity), Stop: make(chan struct{})}
}

// Close releases every blocked load with context.Canceled. It is idempotent.
func (b *Barrier) Close() { b.once.Do(func() { close(b.Stop) }) }

// Load counts the call, announces it on Started and waits for its release.
func (b *Barrier) Load(ctx context.Context, ref model.SessionRef) (*model.Transcript, error) {
	b.Calls.Add(1)
	a := &LoadAttempt{Ref: ref, Context: ctx, reply: make(chan LoadReply, 1)}
	cancel := ctx.Done()
	if b.IgnoreCancellation {
		cancel = nil
	}
	select {
	case b.Started <- a:
	case <-cancel:
		return nil, ctx.Err()
	case <-b.Stop:
		return nil, context.Canceled
	}
	select {
	case r := <-a.reply:
		if r.forward {
			return b.Provider.Load(ctx, ref)
		}
		return r.Transcript, r.Err
	case <-cancel:
		return nil, ctx.Err()
	case <-b.Stop:
		return nil, context.Canceled
	}
}
