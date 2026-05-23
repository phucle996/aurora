package iamCache

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	goredis "github.com/redis/go-redis/v9"
)

type RbacSyncStore interface {
	Subscribe(ctx context.Context) *goredis.PubSub
	LoadEpoch(ctx context.Context) (int64, error)
}

type RedisRbacSyncStore struct{ rdb *goredis.Client }

func NewRedisRbacSyncStore(rdb *goredis.Client) *RedisRbacSyncStore {
	if rdb == nil {
		return nil
	}
	return &RedisRbacSyncStore{rdb: rdb}
}

func (s *RedisRbacSyncStore) Subscribe(ctx context.Context) *goredis.PubSub {
	if s == nil || s.rdb == nil {
		return nil
	}
	return s.rdb.Subscribe(ctx, RbacInvalidateChannel)
}

func (s *RedisRbacSyncStore) LoadEpoch(ctx context.Context) (int64, error) {
	if s == nil || s.rdb == nil {
		return 0, nil
	}
	raw, err := s.rdb.Get(ctx, RbacEpochKey).Result()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return 0, nil
		}
		return 0, fmt.Errorf("rbac cache sync: load epoch: %w", err)
	}
	epoch, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("rbac cache sync: parse epoch: %w", err)
	}
	return epoch, nil
}
