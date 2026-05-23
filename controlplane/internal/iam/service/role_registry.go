package iamSvcImpl

import (
	"strings"
	"sync"
	"time"

	iamSvcInterface "controlplane/internal/iam/domain/service"
)

type roleRegistryItem struct {
	entry     iamSvcInterface.RoleEntry
	expiresAt time.Time
}

type RoleRegistry struct {
	ttl   time.Duration
	mu    sync.RWMutex
	items map[string]roleRegistryItem
}

func NewRoleRegistry(ttl time.Duration) *RoleRegistry {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	return &RoleRegistry{ttl: ttl, items: make(map[string]roleRegistryItem)}
}

func (r *RoleRegistry) Get(role string) (iamSvcInterface.RoleEntry, bool) {
	if r == nil {
		return iamSvcInterface.RoleEntry{}, false
	}
	key := strings.TrimSpace(strings.ToLower(role))
	if key == "" {
		return iamSvcInterface.RoleEntry{}, false
	}

	r.mu.RLock()
	item, ok := r.items[key]
	r.mu.RUnlock()
	if !ok {
		return iamSvcInterface.RoleEntry{}, false
	}
	if time.Now().After(item.expiresAt) {
		r.Invalidate(key)
		return iamSvcInterface.RoleEntry{}, false
	}
	return item.entry, true
}

func (r *RoleRegistry) Set(role string, entry iamSvcInterface.RoleEntry) {
	if r == nil {
		return
	}
	key := strings.TrimSpace(strings.ToLower(role))
	if key == "" {
		return
	}
	r.mu.Lock()
	r.items[key] = roleRegistryItem{entry: entry, expiresAt: time.Now().Add(r.ttl)}
	r.mu.Unlock()
}

func (r *RoleRegistry) Invalidate(role string) {
	if r == nil {
		return
	}
	key := strings.TrimSpace(strings.ToLower(role))
	if key == "" {
		return
	}
	r.mu.Lock()
	delete(r.items, key)
	r.mu.Unlock()
}

func (r *RoleRegistry) InvalidateAll() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.items = make(map[string]roleRegistryItem)
	r.mu.Unlock()
}
