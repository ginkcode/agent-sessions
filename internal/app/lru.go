package app

import (
	"container/list"
	"sync"
)

// lru is a mutex-protected LRU cache keyed by a comparable key.
type lru[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	order    *list.List          // front = most recently used
	entries  map[K]*list.Element // key -> element holding lruEntry
}

type lruEntry[K comparable, V any] struct {
	key   K
	value V
}

// newLRU creates an LRU cache holding at most capacity entries.
// A capacity below one disables retention entirely.
func newLRU[K comparable, V any](capacity int) *lru[K, V] {
	if capacity < 1 {
		capacity = 1
	}
	return &lru[K, V]{
		capacity: capacity,
		order:    list.New(),
		entries:  make(map[K]*list.Element, capacity),
	}
}

// get returns the cached value and marks it most recently used.
func (c *lru[K, V]) get(key K) (V, bool) {
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

// put inserts or refreshes a value, evicting the least recently used entry
// once capacity is exceeded.
func (c *lru[K, V]) put(key K, value V) {
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

// len returns the number of cached entries.
func (c *lru[K, V]) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// evict removes a key from the cache, if present.
func (c *lru[K, V]) evict(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		c.order.Remove(el)
		delete(c.entries, key)
	}
}
