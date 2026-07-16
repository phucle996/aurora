package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"cost-manager/api/internal/domain/entity"
	domainservice "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/transport/pubsub"
	"cost-manager/api/pkg/apperr"
	"github.com/redis/go-redis/v9"
)

type ZoneServiceImpl struct {
	natsClient  pubsub.ZoneNatsClient
	redisClient *redis.Client

	// L1 Cache — in-memory, per-instance
	l1Mutex      sync.RWMutex
	l1Zones      []entity.Zone
	l1Expiration time.Time
}

func NewZoneService(nc pubsub.ZoneNatsClient, rc *redis.Client) domainservice.ZoneService {
	return &ZoneServiceImpl{
		natsClient:  nc,
		redisClient: rc,
	}
}

const (
	redisZoneCacheKey = "cost:cache:zones"
	cacheTTL          = 5 * time.Minute
)

func (s *ZoneServiceImpl) ListZones(ctx context.Context) ([]entity.Zone, error) {
	// 1. L1 Cache (In-memory)
	s.l1Mutex.RLock()
	if s.l1Zones != nil && time.Now().Before(s.l1Expiration) {
		zones := s.l1Zones
		s.l1Mutex.RUnlock()
		return zones, nil
	}
	s.l1Mutex.RUnlock()

	// 2. L2 Cache (Redis)
	if s.redisClient != nil {
		data, err := s.redisClient.Get(ctx, redisZoneCacheKey).Bytes()
		if err == nil {
			var zones []entity.Zone
			if jsonErr := json.Unmarshal(data, &zones); jsonErr == nil {
				s.l1Mutex.Lock()
				s.l1Zones = zones
				s.l1Expiration = time.Now().Add(cacheTTL)
				s.l1Mutex.Unlock()
				return zones, nil
			}
		}
	}

	// 3. Fetch từ Controlplane qua NATS pubsub client
	zones, err := s.natsClient.GetZoneList(ctx)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("nats zone fetch failed: %w", err), "zone_fetch_failed")
	}

	// 4. Populate L1 + L2 Cache
	s.l1Mutex.Lock()
	s.l1Zones = zones
	s.l1Expiration = time.Now().Add(cacheTTL)
	s.l1Mutex.Unlock()

	if s.redisClient != nil {
		if data, err := json.Marshal(zones); err == nil {
			_ = s.redisClient.Set(ctx, redisZoneCacheKey, data, cacheTTL).Err()
		}
	}

	return zones, nil
}
