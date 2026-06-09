package iamCache

import (
	"context"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

type OneTimeTokenCache interface {
	SetHashedToken(ctx context.Context, purpose string, userID uuid.UUID, tokenHash string, ttl time.Duration) error
	ConsumeHashedToken(ctx context.Context, purpose string, userID uuid.UUID, tokenHash string) (bool, error)
}

type noopOneTimeTokenCache struct{}

func (noopOneTimeTokenCache) SetHashedToken(context.Context, string, uuid.UUID, string, time.Duration) error {
	return iamTaxonomy.ErrOneTimeTokenCacheUnavailable
}

func (noopOneTimeTokenCache) ConsumeHashedToken(context.Context, string, uuid.UUID, string) (bool, error) {
	return false, iamTaxonomy.ErrOneTimeTokenCacheUnavailable
}

type redisOneTimeTokenCache struct {
	rdb *goredis.Client
}

func NewOneTimeTokenCache(rdb *goredis.Client) OneTimeTokenCache {
	return &redisOneTimeTokenCache{rdb: rdb}
}

func oneTimeTokenKey(purpose string, userID uuid.UUID) string {
	return fmt.Sprintf("iam:ott:%s:%s", strings.TrimSpace(purpose), strings.TrimSpace(userID.String()))
}

func (c *redisOneTimeTokenCache) SetHashedToken(ctx context.Context, purpose string, userID uuid.UUID, tokenHash string, ttl time.Duration) error {
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

func (c *redisOneTimeTokenCache) ConsumeHashedToken(ctx context.Context, purpose string, userID uuid.UUID, tokenHash string) (bool, error) {
	key := oneTimeTokenKey(purpose, userID)
	result, err := consumeOneTimeTokenScript.Run(ctx, c.rdb, []string{key}, strings.TrimSpace(tokenHash)).Int64()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}
