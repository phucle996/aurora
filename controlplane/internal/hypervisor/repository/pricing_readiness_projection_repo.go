package hypervisorRepoImpl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"

	goredis "github.com/redis/go-redis/v9"
)

const (
	hypervisorPricingReadinessProjectionKey      = "controlplane:hypervisor:pricing-readiness:v1"
	hypervisorPricingReadinessProjectionFenceKey = "controlplane:hypervisor:pricing-readiness:fence:v1"
)

type HypervisorPricingReadinessProjectionRepo struct{ redis *goredis.Client }

func NewHypervisorPricingReadinessProjectionRepo(redisClient *goredis.Client) *HypervisorPricingReadinessProjectionRepo {
	return &HypervisorPricingReadinessProjectionRepo{redis: redisClient}
}

type hypervisorPricingReadinessRecord struct {
	SchemaVersion int      `json:"schema_version"`
	Ready         bool     `json:"ready"`
	Missing       []string `json:"missing"`
	ObservedAt    string   `json:"observed_at"`
	ValidUntil    string   `json:"valid_until"`
	Fingerprint   string   `json:"fingerprint"`
}

func (r *HypervisorPricingReadinessProjectionRepo) UpsertPricingReadiness(ctx context.Context, projection *hypervisorEntity.PricingReadinessProjection) error {
	payload, err := json.Marshal(hypervisorPricingReadinessRecord{
		SchemaVersion: projection.SchemaVersion, Ready: projection.Ready,
		Missing: projection.Missing, ObservedAt: projection.ObservedAt.Format(time.RFC3339Nano),
		ValidUntil: projection.ValidUntil.Format(time.RFC3339Nano), Fingerprint: projection.Fingerprint,
	})
	if err != nil {
		return fmt.Errorf("encode Hypervisor pricing readiness projection: %w", err)
	}
	ttl := time.Until(projection.ValidUntil)
	_, err = r.redis.Eval(ctx, `
		local current = redis.call('GET', KEYS[2])
		if current and tonumber(current) >= tonumber(ARGV[1]) then return 0 end
		redis.call('PSETEX', KEYS[1], ARGV[2], ARGV[3])
		redis.call('PSETEX', KEYS[2], ARGV[2], ARGV[1])
		return 1`, []string{hypervisorPricingReadinessProjectionKey, hypervisorPricingReadinessProjectionFenceKey},
		projection.ObservedAt.UnixMilli(), ttl.Milliseconds(), payload).Result()
	if err != nil {
		return fmt.Errorf("upsert Hypervisor pricing readiness projection: %w", err)
	}
	return nil
}

func (r *HypervisorPricingReadinessProjectionRepo) ReadPricingReadiness(ctx context.Context) (*hypervisorEntity.PricingReadinessProjection, error) {
	raw, err := r.redis.Get(ctx, hypervisorPricingReadinessProjectionKey).Bytes()
	if err != nil {
		return nil, err
	}
	var record hypervisorPricingReadinessRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("decode Hypervisor pricing readiness projection: %w", err)
	}
	observedAt, err := time.Parse(time.RFC3339Nano, record.ObservedAt)
	if err != nil {
		return nil, err
	}
	validUntil, err := time.Parse(time.RFC3339Nano, record.ValidUntil)
	if err != nil {
		return nil, err
	}
	return &hypervisorEntity.PricingReadinessProjection{
		SchemaVersion: record.SchemaVersion, Ready: record.Ready,
		Missing: record.Missing, ObservedAt: observedAt.UTC(), ValidUntil: validUntil.UTC(),
		Fingerprint: record.Fingerprint,
	}, nil
}
