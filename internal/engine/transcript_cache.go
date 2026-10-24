package engine

import (
	"sync"

	"github.com/ginkcode/agent-sessions/internal/model"
)

// transcriptCache serializes lookup, load identity and publication with
// invalidation. Provider I/O must happen between begin and finish, outside mu.
// It owns no goroutines or contexts: direct Service callers retain their normal
// cancellation/lifecycle semantics. Loads are deliberately not coalesced.
type transcriptCache struct {
	mu     sync.Mutex
	lru    *LRU[string, *model.Transcript]
	epoch  uint64
	active map[string]*transcriptToken
}

// Tokens exist only while loads are outstanding. Pointer identity prevents a
// removed/reappearing key from reusing an old identity, without tombstones for
// every key ever seen. The non-zero-sized allocation stays alive in each load.
type transcriptToken struct {
	pending int
}

type transcriptLoad struct {
	token *transcriptToken
	epoch uint64
}

func newTranscriptCache(capacity int) *transcriptCache {
	return &transcriptCache{
		lru:    NewLRU[string, *model.Transcript](capacity),
		active: make(map[string]*transcriptToken),
	}
}

func (c *transcriptCache) begin(key string) (*model.Transcript, transcriptLoad) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tr, ok := c.lru.Get(key); ok {
		return tr, transcriptLoad{}
	}
	token := c.active[key]
	if token == nil {
		token = &transcriptToken{}
		c.active[key] = token
	}
	token.pending++
	return nil, transcriptLoad{token: token, epoch: c.epoch}
}

func (c *transcriptCache) finish(key string, load transcriptLoad, tr *model.Transcript, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if load.epoch != c.epoch || c.active[key] != load.token {
		return
	}
	if err == nil {
		c.lru.Put(key, tr)
	}
	load.token.pending--
	if load.token.pending == 0 {
		delete(c.active, key)
	}
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
	c.epoch++
	clear(c.active)
	c.lru.Clear()
}
