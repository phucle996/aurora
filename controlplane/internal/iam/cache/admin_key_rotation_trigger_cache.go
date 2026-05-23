package iamCache

import (
	"context"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type AdminKeyRotationTriggerCache interface {
	SetRotationRequired(ctx context.Context, ttl time.Duration) error
	HasRotationRequired(ctx context.Context) (bool, error)
	ClearRotationRequired(ctx context.Context) error
}

type adminKeyRotationTriggerCache struct {
	rdb *goredis.Client
}

func NewAdminKeyRotationTriggerCache(rdb *goredis.Client) AdminKeyRotationTriggerCache {
	return &adminKeyRotationTriggerCache{rdb: rdb}
}

func (c *adminKeyRotationTriggerCache) SetRotationRequired(ctx context.Context, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return fmt.Errorf("iam cache: trigger ttl must be positive")
	}
	return c.rdb.SetNX(ctx, c.key(), "1", ttl).Err()
}

func (c *adminKeyRotationTriggerCache) HasRotationRequired(ctx context.Context) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, fmt.Errorf("iam cache: redis client is required")
	}
	v, err := c.rdb.Exists(ctx, c.key()).Result()
	if err != nil {
		return false, err
	}
	return v > 0, nil
}

func (c *adminKeyRotationTriggerCache) ClearRotationRequired(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}
	return c.rdb.Del(ctx, c.key()).Err()
}

func (c *adminKeyRotationTriggerCache) key() string {
	return strings.TrimSpace("iam:admin:key:rotation:required")
}
