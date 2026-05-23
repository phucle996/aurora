package iamCache

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	RbacInvalidateChannel = "iam:rbac:invalidate"
	RbacEpochKey          = "iam:rbac:epoch"
)

type RbacInvalidateKind string

const (
	RbacInvalidateRole RbacInvalidateKind = "role"
	RbacInvalidateAll  RbacInvalidateKind = "all"
)

type RbacInvalidateEvent struct {
	Kind        RbacInvalidateKind `json:"kind"`
	Role        string             `json:"role,omitempty"`
	Epoch       int64              `json:"epoch"`
	PublishedAt time.Time          `json:"published_at"`
}

type RbacCacheBus interface {
	PublishInvalidateRole(ctx context.Context, role string) error
	PublishInvalidateAll(ctx context.Context) error
}

type RedisRbacCacheBus struct{ rdb *goredis.Client }

func NewRedisRbacCacheBus(rdb *goredis.Client) *RedisRbacCacheBus {
	if rdb == nil {
		return nil
	}
	return &RedisRbacCacheBus{rdb: rdb}
}

func (b *RedisRbacCacheBus) PublishInvalidateRole(ctx context.Context, role string) error {
	if b == nil || b.rdb == nil {
		return nil
	}
	return b.publish(ctx, RbacInvalidateEvent{Kind: RbacInvalidateRole, Role: strings.TrimSpace(strings.ToLower(role))})
}

func (b *RedisRbacCacheBus) PublishInvalidateAll(ctx context.Context) error {
	if b == nil || b.rdb == nil {
		return nil
	}
	return b.publish(ctx, RbacInvalidateEvent{Kind: RbacInvalidateAll})
}

func (b *RedisRbacCacheBus) publish(ctx context.Context, event RbacInvalidateEvent) error {
	epoch, err := b.rdb.Incr(ctx, RbacEpochKey).Result()
	if err != nil {
		return fmt.Errorf("rbac cache bus: bump epoch: %w", err)
	}
	event.Epoch = epoch
	event.PublishedAt = time.Now().UTC()
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("rbac cache bus: marshal: %w", err)
	}
	if err := b.rdb.Publish(ctx, RbacInvalidateChannel, payload).Err(); err != nil {
		return fmt.Errorf("rbac cache bus: publish: %w", err)
	}
	return nil
}
