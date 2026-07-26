package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	pricingCacheInvalidationChannel = "billing.pricing.tier_cache.invalidate"
	pricingCacheKeyPrefix           = "cost-manager:pricing:active:v1"
	pricingCacheL1TTL               = time.Minute
	pricingCacheL2TTL               = 5 * time.Minute
)

type pricingCacheItem struct {
	snapshot  *entity.PricingSnapshot
	expiresAt time.Time
}

// pricingCache tăng tốc read-only estimate nhưng không trở thành pricing SoT.
// L1 mất khi pod restart; L2 mất thì repository vẫn rebuild từ PostgreSQL.
type pricingCache struct {
	repo        billingRepoInterface.TierRepository
	redisClient *redis.Client
	sfGroup     singleflight.Group

	mu sync.RWMutex
	l1 map[entity.ServiceType]pricingCacheItem
	// generation fences an in-flight old DB/L2 read from repopulating L1 after invalidation.
	generation uint64
}

type pricingCachePayload struct {
	TierID        string                  `json:"tier_id"`
	TierVersionID string                  `json:"tier_version_id"`
	Code          string                  `json:"code"`
	ServiceType   string                  `json:"service_type"`
	VersionNumber int                     `json:"version_number"`
	EffectiveFrom time.Time               `json:"effective_from"`
	EffectiveTo   *time.Time              `json:"effective_to,omitempty"`
	Checksum      string                  `json:"checksum"`
	Currency      string                  `json:"currency"`
	Ranges        []entity.TierRangeInput `json:"ranges"`
}

// get returns a shared immutable snapshot. Callers must never mutate Ranges; this keeps the L1 hit path
// to one RLock/map lookup without allocating a defensive copy for every estimate.
func (c *pricingCache) get(ctx context.Context, serviceType entity.ServiceType) (*entity.PricingSnapshot, error) {
	now := time.Now()
	c.mu.RLock()
	item, ok := c.l1[serviceType]
	c.mu.RUnlock()
	if ok && now.Before(item.expiresAt) && item.snapshot != nil && !item.snapshot.EffectiveFrom.After(now) &&
		(item.snapshot.EffectiveTo == nil || now.Before(*item.snapshot.EffectiveTo)) {
		return item.snapshot, nil
	}

	cacheKey := fmt.Sprintf("%s:%s", pricingCacheKeyPrefix, serviceType)
	value, err, _ := c.sfGroup.Do(cacheKey, func() (any, error) {
		// A caller may have missed L1 immediately before the previous singleflight completed.
		// Re-check inside the flight and fill L1 before returning to close that stampede window.
		currentTime := time.Now()
		c.mu.RLock()
		currentItem, l1Ready := c.l1[serviceType]
		loadGeneration := c.generation
		c.mu.RUnlock()
		if l1Ready && currentTime.Before(currentItem.expiresAt) && currentItem.snapshot != nil &&
			!currentItem.snapshot.EffectiveFrom.After(currentTime) &&
			(currentItem.snapshot.EffectiveTo == nil || currentTime.Before(*currentItem.snapshot.EffectiveTo)) {
			return currentItem.snapshot, nil
		}
		// Re-check after joining singleflight: another local request may have filled L2.
		if c.redisClient != nil {
			if raw, redisErr := c.redisClient.Get(ctx, cacheKey).Bytes(); redisErr == nil {
				var payload pricingCachePayload
				if decodeErr := json.Unmarshal(raw, &payload); decodeErr == nil {
					tierID, tierIDErr := uuid.Parse(payload.TierID)
					versionID, versionIDErr := uuid.Parse(payload.TierVersionID)
					snapshot := &entity.PricingSnapshot{
						TierID: tierID, TierVersionID: versionID, Code: payload.Code,
						ServiceType: entity.ServiceType(payload.ServiceType), VersionNumber: payload.VersionNumber,
						EffectiveFrom: payload.EffectiveFrom, EffectiveTo: payload.EffectiveTo,
						Checksum: payload.Checksum, Currency: payload.Currency,
						Ranges: append([]entity.TierRangeInput(nil), payload.Ranges...),
					}
					cacheValid := tierIDErr == nil && versionIDErr == nil && tierID != uuid.Nil && versionID != uuid.Nil &&
						snapshot.Code != "" && snapshot.Currency != "" && snapshot.VersionNumber >= 1 &&
						!snapshot.EffectiveFrom.IsZero() && (len(snapshot.Checksum) == 32 || len(snapshot.Checksum) == 64) &&
						(snapshot.EffectiveTo == nil || snapshot.EffectiveTo.After(snapshot.EffectiveFrom))
					switch snapshot.ServiceType {
					case entity.ServiceTypeStorage, entity.ServiceTypeNetworkIn, entity.ServiceTypeNetworkOut, entity.ServiceTypeVM:
					default:
						cacheValid = false
					}
					// Đây là integrity fence cho Shared L2 bytes, không phải request validation.
					// Range phải phủ liên tục [0, infinity) trước khi bytes cache được dùng để báo giá.
					if len(snapshot.Ranges) == 0 || snapshot.Ranges[0].RangeStart != 0 {
						cacheValid = false
					}
					for index, tierRange := range snapshot.Ranges {
						if tierRange.RangeStart < 0 || tierRange.BaseUnitPrice < 0 ||
							(tierRange.RangeEnd != 0 && tierRange.RangeEnd <= tierRange.RangeStart) {
							cacheValid = false
							break
						}
						if index == len(snapshot.Ranges)-1 {
							if tierRange.RangeEnd != 0 {
								cacheValid = false
							}
							continue
						}
						if tierRange.RangeEnd == 0 || tierRange.RangeEnd != snapshot.Ranges[index+1].RangeStart {
							cacheValid = false
							break
						}
					}
					if cacheValid && len(snapshot.Checksum) == 64 {
						checksum := sha256.New()
						_, _ = fmt.Fprintf(checksum, "%s\x00%s\x00", snapshot.Code, string(snapshot.ServiceType))
						for _, tierRange := range snapshot.Ranges {
							_, _ = fmt.Fprintf(checksum, "%d:%d:%d;", tierRange.RangeStart, tierRange.RangeEnd, tierRange.BaseUnitPrice)
						}
						cacheValid = fmt.Sprintf("%x", checksum.Sum(nil)) == snapshot.Checksum
					}
					effectiveNow := time.Now()
					if cacheValid && snapshot.ServiceType == serviceType &&
						!snapshot.EffectiveFrom.After(effectiveNow) &&
						(snapshot.EffectiveTo == nil || effectiveNow.Before(*snapshot.EffectiveTo)) {
						ttl := pricingCacheL1TTL
						if snapshot.EffectiveTo != nil && snapshot.EffectiveTo.Sub(effectiveNow) < ttl {
							ttl = snapshot.EffectiveTo.Sub(effectiveNow)
						}
						retained := false
						if ttl > 0 {
							c.mu.Lock()
							if c.generation == loadGeneration {
								c.l1[serviceType] = pricingCacheItem{snapshot: snapshot, expiresAt: effectiveNow.Add(ttl)}
								retained = true
							}
							c.mu.Unlock()
						}
						if retained {
							return snapshot, nil
						}
					}
				}
			}
		}
		snapshot, repoErr := c.repo.GetActivePricingSnapshot(ctx, serviceType)
		if repoErr != nil {
			return nil, repoErr
		}
		c.mu.RLock()
		generationCurrent := c.generation == loadGeneration
		c.mu.RUnlock()
		if c.redisClient != nil && generationCurrent {
			payload, marshalErr := json.Marshal(pricingCachePayload{
				TierID: snapshot.TierID.String(), TierVersionID: snapshot.TierVersionID.String(),
				Code: snapshot.Code, ServiceType: string(snapshot.ServiceType), VersionNumber: snapshot.VersionNumber,
				EffectiveFrom: snapshot.EffectiveFrom, EffectiveTo: snapshot.EffectiveTo, Checksum: snapshot.Checksum,
				Currency: snapshot.Currency, Ranges: snapshot.Ranges,
			})
			if marshalErr == nil {
				// Cache write is best effort; PostgreSQL remains authoritative on failure.
				now := time.Now()
				ttl := pricingCacheL2TTL
				if snapshot.EffectiveTo != nil && snapshot.EffectiveTo.Sub(now) < ttl {
					ttl = snapshot.EffectiveTo.Sub(now)
				}
				if ttl > 0 {
					_ = c.redisClient.Set(ctx, cacheKey, payload, ttl).Err()
				}
			}
		}
		// If invalidation raced this load, serve the already-started request but do not retain stale L1 state.
		now = time.Now()
		ttl := pricingCacheL1TTL
		if snapshot.EffectiveTo != nil && snapshot.EffectiveTo.Sub(now) < ttl {
			ttl = snapshot.EffectiveTo.Sub(now)
		}
		if ttl > 0 {
			c.mu.Lock()
			if c.generation == loadGeneration {
				c.l1[serviceType] = pricingCacheItem{snapshot: snapshot, expiresAt: now.Add(ttl)}
			}
			c.mu.Unlock()
		}
		return snapshot, nil
	})
	if err != nil {
		return nil, err
	}
	snapshot, ok := value.(*entity.PricingSnapshot)
	if !ok || snapshot == nil {
		return nil, fmt.Errorf("pricing cache returned unexpected value %T", value)
	}
	return snapshot, nil
}

func (c *pricingCache) invalidate(ctx context.Context) {
	c.mu.Lock()
	c.generation++
	c.l1 = make(map[entity.ServiceType]pricingCacheItem)
	c.mu.Unlock()
	if c.redisClient != nil {
		for _, serviceType := range []entity.ServiceType{
			entity.ServiceTypeStorage,
			entity.ServiceTypeNetworkIn,
			entity.ServiceTypeNetworkOut,
			entity.ServiceTypeVM,
		} {
			if err := c.redisClient.Del(ctx, fmt.Sprintf("%s:%s", pricingCacheKeyPrefix, serviceType)).Err(); err != nil && ctx.Err() == nil {
				logger.SysWarn("billing.pricing.cache.invalidate", err.Error())
			}
		}
	}
}

// RunInvalidation consumes a non-durable latency hint. TTL/cold-start rebuilds correctness when PubSub is missed.
func (c *pricingCache) runInvalidation(ctx context.Context) {
	if c.redisClient == nil {
		return
	}
	for {
		pubsub := c.redisClient.Subscribe(ctx, pricingCacheInvalidationChannel)
		if _, err := pubsub.Receive(ctx); err != nil {
			_ = pubsub.Close()
			if ctx.Err() != nil {
				return
			}
			logger.SysWarn("billing.pricing.cache.subscribe", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
				continue
			}
		}

		messages := pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				_ = pubsub.Close()
				return
			case _, ok := <-messages:
				if !ok {
					_ = pubsub.Close()
					goto reconnect
				}
				c.invalidate(ctx)
			}
		}
	reconnect:
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
