package coreCache

import (
	"context"
	"strings"
	"sync"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreSvcInterface "controlplane/internal/core/domain/service"
	coreerrorx "controlplane/internal/core/taxonomy"
	coreMetric "controlplane/internal/core/metrics"
	"controlplane/pkg/logger"
)

type runtimeSecretFamilyReader interface {
	GetRuntimeSecretFamily(ctx context.Context, familyCode string) (*coreEntity.RuntimeSecretFamily, error)
}

type cacheEntry struct {
	value    *coreEntity.RuntimeSecretFamily
	loadedAt time.Time
}

// CacheAsideSecretProvider là runtime secret provider dạng cache-aside.
// Thuộc tầng cache/infra, không chứa business policy.
type CacheAsideSecretProvider struct {
	readService runtimeSecretFamilyReader
	cacheTTL    time.Duration
	mu          sync.RWMutex
	cache       map[string]*cacheEntry
	locksMu     sync.Mutex
	locks       map[string]*sync.Mutex
}

func NewCacheAsideSecretProvider(readService runtimeSecretFamilyReader) coreSvcInterface.RuntimeSecretProvider {
	return NewCacheAsideSecretProviderWithTTL(readService, 30*time.Second)
}

func NewCacheAsideSecretProviderWithTTL(readService runtimeSecretFamilyReader, cacheTTL time.Duration) coreSvcInterface.RuntimeSecretProvider {
	if readService == nil {
		panic("core secret provider: runtime secret family reader is required")
	}
	if cacheTTL <= 0 {
		cacheTTL = 30 * time.Second
	}
	return &CacheAsideSecretProvider{
		readService: readService,
		cacheTTL:    cacheTTL,
		cache:       make(map[string]*cacheEntry),
		locks:       make(map[string]*sync.Mutex),
	}
}

func (p *CacheAsideSecretProvider) GetPrimary(ctx context.Context, familyCode string) (*coreEntity.RuntimeSecret, error) {
	startedAt := time.Now().UTC()

	family, err := p.getFamily(ctx, familyCode)
	if err != nil {
		coreMetric.ObserveSecretLifecycle("cache_get_primary", strings.TrimSpace(familyCode), "error", startedAt)
		return nil, err
	}
	coreMetric.ObserveSecretLifecycle("cache_get_primary", strings.TrimSpace(familyCode), "ok", startedAt)
	primary := family.Primary
	return &primary, nil
}

func (p *CacheAsideSecretProvider) GetCandidates(ctx context.Context, familyCode string) ([]coreEntity.RuntimeSecret, error) {
	startedAt := time.Now().UTC()

	family, err := p.getFamily(ctx, familyCode)
	if err != nil {
		coreMetric.ObserveSecretLifecycle("cache_get_candidates", strings.TrimSpace(familyCode), "error", startedAt)
		return nil, err
	}
	coreMetric.ObserveSecretLifecycle("cache_get_candidates", strings.TrimSpace(familyCode), "ok", startedAt)
	result := make([]coreEntity.RuntimeSecret, len(family.Candidates))
	copy(result, family.Candidates)
	return result, nil
}

func (p *CacheAsideSecretProvider) Warm(ctx context.Context, familyCode string) error {
	_, err := p.getFamily(ctx, familyCode)
	return err
}

func (p *CacheAsideSecretProvider) Invalidate(familyCode string) {
	startedAt := time.Now().UTC()
	key := strings.TrimSpace(familyCode)
	if key == "" {
		return
	}
	p.mu.Lock()
	delete(p.cache, key)
	p.mu.Unlock()
	coreMetric.ObserveSecretLifecycle("cache_invalidate", strings.TrimSpace(key), "ok", startedAt)
	logger.SysInfoFields("core.secret.cache_invalidate", "invalidated local secret cache", logger.Fields{"family": key})
}

func (p *CacheAsideSecretProvider) getFamily(ctx context.Context, familyCode string) (*coreEntity.RuntimeSecretFamily, error) {
	key := strings.TrimSpace(familyCode)
	if key == "" {
		return nil, coreerrorx.ErrFamilyNotFound
	}
	if cached := p.readCache(key); cached != nil {
		coreMetric.ObserveSecretLifecycle("cache_lookup", strings.TrimSpace(key), "hit", time.Now().UTC())
		return cached, nil
	}
	lock := p.familyLock(key)
	lock.Lock()
	defer lock.Unlock()
	if cached := p.readCache(key); cached != nil {
		coreMetric.ObserveSecretLifecycle("cache_lookup", strings.TrimSpace(key), "hit_after_lock", time.Now().UTC())
		return cached, nil
	}
	cacheMissStarted := time.Now().UTC()
	loaded, err := p.readService.GetRuntimeSecretFamily(ctx, key)
	if err != nil {
		coreMetric.ObserveSecretLifecycle("cache_lookup", strings.TrimSpace(key), "error", cacheMissStarted)
		logger.SysWarnFields("core.secret.cache_reload", "failed to reload secret family from db", err, logger.Fields{"family": key})
		return nil, err
	}
	coreMetric.ObserveSecretLifecycle("cache_lookup", strings.TrimSpace(key), "miss", cacheMissStarted)
	logger.SysInfoFields("core.secret.cache_reload", "reloaded secret family into cache", logger.Fields{"family": key})
	p.mu.Lock()
	p.cache[key] = &cacheEntry{value: loaded, loadedAt: time.Now().UTC()}
	p.mu.Unlock()
	return loaded, nil
}

func (p *CacheAsideSecretProvider) readCache(familyCode string) *coreEntity.RuntimeSecretFamily {
	p.mu.RLock()
	entry := p.cache[familyCode]
	p.mu.RUnlock()
	if entry == nil || entry.value == nil {
		return nil
	}
	if p.cacheTTL > 0 && time.Since(entry.loadedAt) >= p.cacheTTL {
		coreMetric.ObserveSecretLifecycle("cache_lookup", strings.TrimSpace(familyCode), "expired", entry.loadedAt)
		return nil
	}
	copyValue := *entry.value
	copyValue.Candidates = append([]coreEntity.RuntimeSecret(nil), entry.value.Candidates...)
	return &copyValue
}

func (p *CacheAsideSecretProvider) familyLock(familyCode string) *sync.Mutex {
	p.locksMu.Lock()
	defer p.locksMu.Unlock()
	if existing, ok := p.locks[familyCode]; ok {
		return existing
	}
	created := &sync.Mutex{}
	p.locks[familyCode] = created
	return created
}
