package engine

import (
	"container/list"
	"sync"
)

// LRU is a thread-safe, mutex-protected LRU cache keyed by a comparable key.
type LRU[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	order    *list.List          // front = most recently used
	entries  map[K]*list.Element // key -> element holding lruEntry
}

type lruEntry[K comparable, V any] struct {
	key   K
	value V
}

// NewLRU creates an LRU cache holding at most capacity entries.
// A capacity below one disables retention entirely (clamps to 1).
func NewLRU[K comparable, V any](capacity int) *LRU[K, V] {
	if capacity < 1 {
		capacity = 1
	}
	return &LRU[K, V]{
		capacity: capacity,
		order:    list.New(),
		entries:  make(map[K]*list.Element, capacity),
	}
}

// Get returns the cached value and marks it most recently used.
func (c *LRU[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	el, ok := c.entries[key]
	if !ok {
		return zero, false
	}
	c.order.MoveToFront(el)
	return el.Value.(lruEntry[K, V]).value, true
}

func (c *LRU[K, V]) get(key K) (V, bool) {
	return c.Get(key)
}

// Put inserts or refreshes a value, evicting the least recently used entry
// once capacity is exceeded.
func (c *LRU[K, V]) Put(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.order.MoveToFront(el)
		el.Value = lruEntry[K, V]{key: key, value: value}
		return
	}
	c.entries[key] = c.order.PushFront(lruEntry[K, V]{key: key, value: value})
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(lruEntry[K, V]).key)
	}
}

func (c *LRU[K, V]) put(key K, value V) {
	c.Put(key, value)
}

// Len returns the number of cached entries.
func (c *LRU[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

func (c *LRU[K, V]) len() int {
	return c.Len()
}

// Evict removes a key from the cache, if present.
func (c *LRU[K, V]) Evict(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.order.Remove(el)
		delete(c.entries, key)
	}
}

func (c *LRU[K, V]) evict(key K) {
	c.Evict(key)
}
