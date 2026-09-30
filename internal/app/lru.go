package app

import (
	"github.com/ginkcode/agent-sessions/internal/engine"
)

type lru[K comparable, V any] struct {
	*engine.LRU[K, V]
}

func newLRU[K comparable, V any](capacity int) *lru[K, V] {
	return &lru[K, V]{LRU: engine.NewLRU[K, V](capacity)}
}

func (c *lru[K, V]) get(key K) (V, bool) {
	return c.Get(key)
}

func (c *lru[K, V]) put(key K, value V) {
	c.Put(key, value)
}

func (c *lru[K, V]) len() int {
	return c.Len()
}

func (c *lru[K, V]) evict(key K) {
	c.Evict(key)
}
