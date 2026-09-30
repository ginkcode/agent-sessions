package rpc

import (
	"context"
	"fmt"
	"sync"
)

// CancelRegistry tracks cancellable contexts for active in-flight RPC calls.
type CancelRegistry struct {
	mu     sync.Mutex
	active map[string]context.CancelFunc
}

// NewCancelRegistry creates an empty cancel registry.
func NewCancelRegistry() *CancelRegistry {
	return &CancelRegistry{
		active: make(map[string]context.CancelFunc),
	}
}

func idKey(id any) string {
	if id == nil {
		return ""
	}
	return fmt.Sprintf("%v", id)
}

// Register registers a cancel func for the given request ID.
func (r *CancelRegistry) Register(id any, cancel context.CancelFunc) {
	key := idKey(id)
	if key == "" || cancel == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[key] = cancel
}

// Unregister removes a request ID from the registry.
func (r *CancelRegistry) Unregister(id any) {
	key := idKey(id)
	if key == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, key)
}

// Cancel calls the CancelFunc associated with the request ID, if present.
func (r *CancelRegistry) Cancel(id any) bool {
	key := idKey(id)
	if key == "" {
		return false
	}
	r.mu.Lock()
	cancel, ok := r.active[key]
	delete(r.active, key)
	r.mu.Unlock()

	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}
