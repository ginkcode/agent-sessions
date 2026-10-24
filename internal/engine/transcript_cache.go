package engine

import (
	"context"
	"sync"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// transcriptLoadLimit bounds provider I/O, including superseded loads that have
// not returned yet. Waiting for a slot is cancellable and holds no cache lock.
const transcriptLoadLimit = 4

// transcriptCache coordinates the four-entry LRU and current flights. A flight's
// pointer is its unique token: invalidation detaches it without denying existing
// readers their snapshot. Only the current flight may publish or remove itself.
type transcriptCache struct {
	mu     sync.Mutex
	lru    *LRU[string, *model.Transcript]
	active map[string]*transcriptLoad
	owned  map[*transcriptLoad]struct{}
	ctx    context.Context
	cancel context.CancelFunc
	slots  chan struct{}
}

type transcriptLoad struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	waiters int
	tr      *model.Transcript
	err     error
}

func newTranscriptCache(capacity int) *transcriptCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &transcriptCache{lru: NewLRU[string, *model.Transcript](capacity), active: make(map[string]*transcriptLoad), owned: make(map[*transcriptLoad]struct{}), ctx: ctx, cancel: cancel, slots: make(chan struct{}, transcriptLoadLimit)}
}

// start gives a restarted engine a fresh lifetime. Standalone services use a
// background lifetime; their last departing waiter still cancels orphan work.
func (c *transcriptCache) start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
	c.ctx, c.cancel = context.WithCancel(ctx)
}

func (c *transcriptCache) stopLocked() {
	c.cancel()
	for f := range c.owned {
		f.cancel()
	}
	clear(c.active)
	c.lru.Clear()
}

func (c *transcriptCache) stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
}

func (c *transcriptCache) load(ctx context.Context, key string, load func(context.Context) (*model.Transcript, error)) (*model.Transcript, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if err := c.ctx.Err(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if tr, ok := c.lru.Get(key); ok {
		c.mu.Unlock()
		return tr, nil
	}
	f := c.active[key]
	if f == nil {
		workCtx, cancel := context.WithCancel(c.ctx)
		f = &transcriptLoad{ctx: workCtx, cancel: cancel, done: make(chan struct{})}
		c.active[key] = f
		c.owned[f] = struct{}{}
		go c.run(key, f, load)
	}
	f.waiters++
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		c.mu.Lock()
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
			if c.active[key] == f {
				delete(c.active, key)
			}
		}
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-f.ctx.Done():
		// Shutdown must release readers even if a provider ignores cancellation.
		select {
		case <-f.done:
			return f.tr, f.err
		default:
			return nil, f.ctx.Err()
		}
	case <-f.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return f.tr, f.err
	}
}

func (c *transcriptCache) run(key string, f *transcriptLoad, load func(context.Context) (*model.Transcript, error)) {
	var tr *model.Transcript
	var err error
	select {
	case c.slots <- struct{}{}:
		if err = f.ctx.Err(); err == nil {
			tr, err = load(f.ctx)
		}
		<-c.slots
	case <-f.ctx.Done():
		err = f.ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		err = f.ctx.Err()
	}
	if c.active[key] == f {
		if err == nil {
			c.lru.Put(key, tr)
		}
		delete(c.active, key)
	}
	if err != nil {
		tr = nil
	}
	f.tr, f.err = tr, err
	delete(c.owned, f)
	close(f.done)
	f.cancel()
}

func (c *transcriptCache) evict(keys ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range keys {
		delete(c.active, key)
		c.lru.Evict(key)
	}
}

func (c *transcriptCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.active)
	c.lru.Clear()
}
