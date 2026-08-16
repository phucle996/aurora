package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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
	pricingCacheInvalidationChannel   = "billing.pricing.schedule_cache.invalidate"
	pricingCacheKeyPrefix             = "cost-manager:pricing:schedule:v3"
	pricingCacheL1TTL                 = time.Minute
	pricingCacheL2TTL                 = 5 * time.Minute
	pricingSnapshotChecksumTimeLayout = "2006-01-02T15:04:05.000000Z07:00"
)

type pricingCacheItem struct {
	snapshot  *entity.PricingSnapshot
	expiresAt time.Time
}

type pricingCache struct {
	repo        billingRepoInterface.PricingSnapshotRepository
	redisClient *redis.Client
	sfGroup     singleflight.Group

	mu         sync.RWMutex
	l1         map[string]pricingCacheItem
	generation uint64
}

type pricingCachePayload struct {
	PricingScheduleID string                          `json:"pricing_schedule_id"`
	VersionID         string                          `json:"version_id"`
	Code              string                          `json:"code"`
	ChargeKindCode    string                          `json:"charge_kind_code"`
	ModuleCode        string                          `json:"module_code"`
	PricingModel      entity.PricingModel             `json:"pricing_model"`
	RawInputUnit      string                          `json:"raw_input_unit"`
	VersionNumber     int                             `json:"version_number"`
	EffectiveFrom     time.Time                       `json:"effective_from"`
	EffectiveTo       *time.Time                      `json:"effective_to,omitempty"`
	Checksum          string                          `json:"checksum"`
	Currency          string                          `json:"currency"`
	Brackets          []entity.PricingSnapshotBracket `json:"brackets"`
}

func (c *pricingCache) get(ctx context.Context, chargeKind entity.ChargeKindCode, at time.Time) (*entity.PricingSnapshot, error) {
	lookupKey := string(chargeKind)
	now := time.Now().UTC()
	c.mu.RLock()
	item, ok := c.l1[lookupKey]
	c.mu.RUnlock()
	if ok && now.Before(item.expiresAt) && snapshotEffectiveAt(item.snapshot, at) {
		return item.snapshot, nil
	}

	value, err, _ := c.sfGroup.Do(lookupKey, func() (any, error) {
		loadGeneration := func() uint64 {
			c.mu.RLock()
			defer c.mu.RUnlock()
			return c.generation
		}()
		current := time.Now().UTC()
		c.mu.RLock()
		cached, ready := c.l1[lookupKey]
		c.mu.RUnlock()
		if ready && current.Before(cached.expiresAt) && snapshotEffectiveAt(cached.snapshot, at) {
			return cached.snapshot, nil
		}

		cacheKey := fmt.Sprintf("%s:%s", pricingCacheKeyPrefix, lookupKey)
		if c.redisClient != nil {
			if raw, redisErr := c.redisClient.Get(ctx, cacheKey).Bytes(); redisErr == nil {
				if snapshot, decodeErr := decodePricingSnapshot(raw); decodeErr == nil && snapshot.ChargeKindCode == chargeKind && snapshotEffectiveAt(snapshot, at) {
					c.mu.Lock()
					if c.generation == loadGeneration {
						c.l1[lookupKey] = pricingCacheItem{snapshot: snapshot, expiresAt: current.Add(pricingCacheL1TTL)}
					}
					c.mu.Unlock()
					return snapshot, nil
				}
			}
		}

		snapshot, repoErr := c.repo.GetActivePricingSnapshot(ctx, chargeKind, at)
		if repoErr != nil {
			return nil, repoErr
		}
		if err := validateCachedSnapshot(snapshot); err != nil {
			return nil, err
		}
		if c.redisClient != nil && loadGeneration == c.currentGeneration() {
			if payload, marshalErr := json.Marshal(pricingCachePayloadFromSnapshot(snapshot)); marshalErr == nil {
				_ = c.redisClient.Set(ctx, cacheKey, payload, pricingCacheL2TTL).Err()
			}
		}
		c.mu.Lock()
		if c.generation == loadGeneration {
			c.l1[lookupKey] = pricingCacheItem{snapshot: snapshot, expiresAt: time.Now().UTC().Add(pricingCacheL1TTL)}
		}
		c.mu.Unlock()
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

func snapshotEffectiveAt(snapshot *entity.PricingSnapshot, at time.Time) bool {
	return snapshot != nil && !snapshot.EffectiveFrom.After(at) && (snapshot.EffectiveTo == nil || at.Before(*snapshot.EffectiveTo))
}

func (c *pricingCache) currentGeneration() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

func pricingCachePayloadFromSnapshot(snapshot *entity.PricingSnapshot) pricingCachePayload {
	return pricingCachePayload{
		PricingScheduleID: snapshot.PricingScheduleID.String(), VersionID: snapshot.VersionID.String(),
		Code: snapshot.Code, ChargeKindCode: string(snapshot.ChargeKindCode), ModuleCode: snapshot.ModuleCode,
		PricingModel: snapshot.PricingModel,
		RawInputUnit: snapshot.RawInputUnit, VersionNumber: snapshot.VersionNumber,
		EffectiveFrom: snapshot.EffectiveFrom, EffectiveTo: snapshot.EffectiveTo, Checksum: snapshot.Checksum,
		Currency: snapshot.Currency, Brackets: append([]entity.PricingSnapshotBracket(nil), snapshot.Brackets...),
	}
}

func decodePricingSnapshot(raw []byte) (*entity.PricingSnapshot, error) {
	var payload pricingCachePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	scheduleID, err := uuid.Parse(payload.PricingScheduleID)
	if err != nil {
		return nil, err
	}
	versionID, err := uuid.Parse(payload.VersionID)
	if err != nil {
		return nil, err
	}
	snapshot := &entity.PricingSnapshot{
		PricingScheduleID: scheduleID, VersionID: versionID, Code: payload.Code,
		ChargeKindCode: entity.ChargeKindCode(payload.ChargeKindCode), ModuleCode: payload.ModuleCode,
		PricingModel: payload.PricingModel,
		RawInputUnit: payload.RawInputUnit, VersionNumber: payload.VersionNumber,
		EffectiveFrom: payload.EffectiveFrom, EffectiveTo: payload.EffectiveTo, Checksum: payload.Checksum,
		Currency: payload.Currency, Brackets: append([]entity.PricingSnapshotBracket(nil), payload.Brackets...),
	}
	if err := validateCachedSnapshot(snapshot); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func validateCachedSnapshot(snapshot *entity.PricingSnapshot) error {
	if snapshot == nil || snapshot.PricingScheduleID == uuid.Nil || snapshot.VersionID == uuid.Nil || snapshot.Code == "" || snapshot.Currency == "" || snapshot.VersionNumber < 1 || snapshot.PricingModel != entity.PricingModelProgressiveUnit {
		return fmt.Errorf("pricing snapshot is incomplete")
	}
	if err := validatePricingSnapshotBrackets(snapshot.Brackets); err != nil {
		return err
	}
	if pricingSnapshotChecksum(*snapshot) != snapshot.Checksum {
		return fmt.Errorf("pricing snapshot checksum mismatch")
	}
	return nil
}

func pricingSnapshotChecksum(snapshot entity.PricingSnapshot) string {
	hash := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	write(snapshot.Code)
	write(string(snapshot.ChargeKindCode))
	write(string(snapshot.PricingModel))
	write(snapshot.Currency)
	write(snapshot.EffectiveFrom.UTC().Format(pricingSnapshotChecksumTimeLayout))
	write(fmt.Sprintf("%d", snapshot.VersionNumber))
	for _, bracket := range snapshot.Brackets {
		write(fmt.Sprintf("%d", bracket.RangeStartQuantity))
		if bracket.RangeEndQuantity == nil {
			write("infinity")
		} else {
			write(fmt.Sprintf("%d", *bracket.RangeEndQuantity))
		}
		write(fmt.Sprintf("%d", bracket.PriceNumeratorMicroUnits))
		write(fmt.Sprintf("%d", bracket.PriceDenominatorQuantity))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}

func validatePricingSnapshotBrackets(brackets []entity.PricingSnapshotBracket) error {
	if len(brackets) == 0 || brackets[0].RangeStartQuantity != 0 {
		return fmt.Errorf("pricing snapshot brackets must start at zero")
	}
	for index, bracket := range brackets {
		if bracket.RangeStartQuantity < 0 || bracket.PriceNumeratorMicroUnits < 0 || bracket.PriceDenominatorQuantity <= 0 {
			return fmt.Errorf("pricing snapshot bracket is invalid")
		}
		if index == len(brackets)-1 {
			if bracket.RangeEndQuantity != nil {
				return fmt.Errorf("pricing snapshot final bracket must be infinite")
			}
			continue
		}
		if bracket.RangeEndQuantity == nil || *bracket.RangeEndQuantity != brackets[index+1].RangeStartQuantity {
			return fmt.Errorf("pricing snapshot brackets contain a gap or overlap")
		}
	}
	return nil
}

func (c *pricingCache) invalidate(ctx context.Context) {
	c.mu.Lock()
	c.generation++
	c.l1 = make(map[string]pricingCacheItem)
	c.mu.Unlock()
	if c.redisClient == nil {
		return
	}
	for _, chargeKind := range []entity.ChargeKindCode{entity.ChargeKindStorageCapacity, entity.ChargeKindStorageNetworkIn, entity.ChargeKindStorageNetworkOut} {
		keys, _ := c.redisClient.Keys(ctx, fmt.Sprintf("%s:%s*", pricingCacheKeyPrefix, chargeKind)).Result()
		if len(keys) > 0 {
			if err := c.redisClient.Del(ctx, keys...).Err(); err != nil && ctx.Err() == nil {
				logger.SysWarn("billing.pricing.cache.invalidate", err.Error())
			}
		}
	}
}

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
					break
				}
				c.invalidate(ctx)
			}
		}
	}
}
