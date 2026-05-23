package iamCache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"

	goredis "github.com/redis/go-redis/v9"
)

// AdminAPIKeyCache tổng hợp cache layer phục vụ admin API key flow.
//
// Trách nhiệm:
//   - Distributed lock cho recovery code consume (chống replay đồng thời).
//   - Snapshot RAM cho TOTP secret + active admin API key (giảm read DB +
//     decrypt trong burst login).
//
// Boundary:
//   - Không log nghiệp vụ; lỗi trả về cho service map sang errorx.
//   - Không lưu plaintext xuống Redis; secret/key snapshot chỉ tồn tại trong
//     RAM của process gọi.
type AdminAPIKeyCache interface {
	// AcquireRecoveryConsumeLock đặt distributed lock theo hash recovery code,
	// trả về unlock callback. Lock dùng SET NX với owner token để chống unlock
	// nhầm khi TTL hết hạn.
	AcquireRecoveryConsumeLock(ctx context.Context, codeHash string, ttl time.Duration) (unlock func(), err error)

	// GetTOTPSecret trả về secret TOTP đã decrypt nếu cache còn hợp lệ và
	// updatedAt khớp với DB. Cache key dựa vào updatedAt để invalidate khi
	// admin xoay secret.
	GetTOTPSecret(updatedAt time.Time) (secret string, ok bool)

	// SetTOTPSecret lưu snapshot secret đã decrypt với TTL runtime cố định.
	SetTOTPSecret(updatedAt time.Time, secret string, ttl time.Duration)

	// GetActiveAPIKey trả về snapshot active admin API key nếu còn hạn cache
	// và key chưa expired theo ExpiresAt.
	GetActiveAPIKey(now time.Time) (item iamEntity.AdminAPIKey, ok bool)

	// SetActiveAPIKey lưu snapshot active key với TTL runtime; service phải
	// đảm bảo TTL không vượt quá expires_at của key.
	SetActiveAPIKey(item iamEntity.AdminAPIKey, ttl time.Duration)

	// InvalidateActiveAPIKey xoá snapshot active key, dùng sau rotation hoặc
	// khi DB thay đổi bất ngờ.
	InvalidateActiveAPIKey()
}

type cachedAdminTOTPSecret struct {
	secret    string
	updatedAt time.Time
	expiresAt time.Time
}

type cachedActiveAdminAPIKey struct {
	item      iamEntity.AdminAPIKey
	expiresAt time.Time
}

type adminAPIKeyCache struct {
	rdb *goredis.Client

	mu        sync.RWMutex
	totpCache map[string]cachedAdminTOTPSecret
	activeKey *cachedActiveAdminAPIKey
}

// NewAdminAPIKeyCache khởi tạo cache layer cho admin API key flow.
func NewAdminAPIKeyCache(rdb *goredis.Client) AdminAPIKeyCache {
	return &adminAPIKeyCache{
		rdb:       rdb,
		totpCache: make(map[string]cachedAdminTOTPSecret),
	}
}

func (c *adminAPIKeyCache) AcquireRecoveryConsumeLock(ctx context.Context, codeHash string, ttl time.Duration) (func(), error) {
	if c == nil || c.rdb == nil {
		return nil, fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("iam cache: lock ttl must be positive")
	}
	ownerToken, err := adminCacheRandomToken(16)
	if err != nil {
		return nil, err
	}
	key := c.recoveryConsumeLockKey(codeHash)
	ok, err := c.rdb.SetNX(ctx, key, ownerToken, ttl).Result()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("iam cache: recovery consume lock already held")
	}
	unlock := func() {
		script := goredis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("del", KEYS[1])
end
return 0
`)
		_, _ = script.Run(context.Background(), c.rdb, []string{key}, ownerToken).Result()
	}
	return unlock, nil
}

func (c *adminAPIKeyCache) GetTOTPSecret(updatedAt time.Time) (string, bool) {
	cacheKey := updatedAt.UTC().Format(time.RFC3339Nano)
	now := time.Now().UTC()

	c.mu.RLock()
	cached, ok := c.totpCache[cacheKey]
	c.mu.RUnlock()
	if !ok || !now.Before(cached.expiresAt) {
		return "", false
	}
	return cached.secret, true
}

func (c *adminAPIKeyCache) SetTOTPSecret(updatedAt time.Time, secret string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	cacheKey := updatedAt.UTC().Format(time.RFC3339Nano)
	now := time.Now().UTC()

	c.mu.Lock()
	c.totpCache[cacheKey] = cachedAdminTOTPSecret{
		secret:    secret,
		updatedAt: updatedAt.UTC(),
		expiresAt: now.Add(ttl),
	}
	c.mu.Unlock()
}

func (c *adminAPIKeyCache) GetActiveAPIKey(now time.Time) (iamEntity.AdminAPIKey, bool) {
	c.mu.RLock()
	cached := c.activeKey
	c.mu.RUnlock()
	if cached == nil || !now.Before(cached.expiresAt) {
		return iamEntity.AdminAPIKey{}, false
	}
	if !cached.item.ExpiresAt.After(now) {
		return iamEntity.AdminAPIKey{}, false
	}
	return cached.item, true
}

func (c *adminAPIKeyCache) SetActiveAPIKey(item iamEntity.AdminAPIKey, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	now := time.Now().UTC()
	c.mu.Lock()
	c.activeKey = &cachedActiveAdminAPIKey{
		item:      item,
		expiresAt: now.Add(ttl),
	}
	c.mu.Unlock()
}

func (c *adminAPIKeyCache) InvalidateActiveAPIKey() {
	c.mu.Lock()
	c.activeKey = nil
	c.mu.Unlock()
}

func (c *adminAPIKeyCache) recoveryConsumeLockKey(codeHash string) string {
	return "iam:admin:recovery:consume:" + strings.TrimSpace(codeHash)
}

func adminCacheRandomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
