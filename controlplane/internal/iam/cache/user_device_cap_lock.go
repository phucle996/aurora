package iamCache

import (
	"context"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// UserDeviceCapLock cung cấp short-lived advisory lock per-user để dồn các
// login đồng thời về 1 worker chạy CTE evict cap (BR-009). Không dùng làm SoT.
type UserDeviceCapLock interface {
	TryAcquire(ctx context.Context, userID string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, userID string) error
}

type userDeviceCapLock struct {
	rdb *goredis.Client
}

// NewUserDeviceCapLock khởi tạo Redis-backed cap lock cho user device flow.
func NewUserDeviceCapLock(rdb *goredis.Client) UserDeviceCapLock {
	return &userDeviceCapLock{rdb: rdb}
}

func (c *userDeviceCapLock) TryAcquire(ctx context.Context, userID string, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return false, fmt.Errorf("iam cache: ttl must be positive")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, fmt.Errorf("iam cache: user id is required")
	}
	ok, err := c.rdb.SetNX(ctx, c.key(userID), "1", ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

func (c *userDeviceCapLock) Release(ctx context.Context, userID string) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("iam cache: user id is required")
	}
	return c.rdb.Del(ctx, c.key(userID)).Err()
}

func (c *userDeviceCapLock) key(userID string) string {
	return "iam:user:device:cap_lock:" + strings.TrimSpace(userID)
}
