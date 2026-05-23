package iamCache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

var (
	ErrOneTimeTokenCacheUnavailable = errors.New("iam cache: one-time token cache unavailable")
)

type OneTimeTokenCache interface {
	SetHashedToken(ctx context.Context, purpose string, userID string, tokenHash string, ttl time.Duration) error
	ConsumeHashedToken(ctx context.Context, purpose string, userID string, tokenHash string) (bool, error)
}

type noopOneTimeTokenCache struct{}

func (noopOneTimeTokenCache) SetHashedToken(context.Context, string, string, string, time.Duration) error {
	return ErrOneTimeTokenCacheUnavailable
}

func (noopOneTimeTokenCache) ConsumeHashedToken(context.Context, string, string, string) (bool, error) {
	return false, ErrOneTimeTokenCacheUnavailable
}

type redisOneTimeTokenCache struct {
	rdb *goredis.Client
}

func NewOneTimeTokenCache(rdb *goredis.Client) OneTimeTokenCache {
	if rdb == nil {
		return noopOneTimeTokenCache{}
	}
	return &redisOneTimeTokenCache{rdb: rdb}
}

func oneTimeTokenKey(purpose string, userID string) string {
	return fmt.Sprintf("iam:ott:%s:%s", strings.TrimSpace(purpose), strings.TrimSpace(userID))
}

func (c *redisOneTimeTokenCache) SetHashedToken(ctx context.Context, purpose string, userID string, tokenHash string, ttl time.Duration) error {
	key := oneTimeTokenKey(purpose, userID)
	return c.rdb.Set(ctx, key, strings.TrimSpace(tokenHash), ttl).Err()
}

var consumeOneTimeTokenScript = goredis.NewScript(`
local key = KEYS[1]
local expected = ARGV[1]
local current = redis.call("GET", key)
if not current then
  return 0
end
if current ~= expected then
  return 0
end
return redis.call("DEL", key)
`)

func (c *redisOneTimeTokenCache) ConsumeHashedToken(ctx context.Context, purpose string, userID string, tokenHash string) (bool, error) {
	key := oneTimeTokenKey(purpose, userID)
	result, err := consumeOneTimeTokenScript.Run(ctx, c.rdb, []string{key}, strings.TrimSpace(tokenHash)).Int64()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}
