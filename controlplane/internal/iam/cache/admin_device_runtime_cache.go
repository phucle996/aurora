package iamCache

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/security"

	goredis "github.com/redis/go-redis/v9"
)

type AdminDeviceRuntimeCache interface {
	SetDeviceRuntime(ctx context.Context, runtime AdminDeviceRuntime, ttl time.Duration) error
	VerifyDeviceSecret(ctx context.Context, deviceID string, rawDeviceSecret string) (bool, error)
	GetDeviceRuntime(ctx context.Context, deviceID string) (*AdminDeviceRuntime, error)
	TouchDeviceSecret(ctx context.Context, deviceID string, ttl time.Duration) error
	CompareAndTouchDeviceRuntime(ctx context.Context, deviceID string, expectedVersion int64, ttl time.Duration, ip *string, userAgent *string) (bool, error)
	ScanDeviceRuntimes(ctx context.Context, limit int) ([]AdminDeviceRuntime, error)
	DeleteDeviceSecret(ctx context.Context, deviceID string) error
}

type AdminDeviceRuntime struct {
	DeviceID         string `json:"device_id"`
	DeviceSecretHash string `json:"device_secret_hash"`
	TrackedDeviceID  string `json:"tracked_device_id"`
	// DevicePublicKey giúp critical signature guard verify từ Redis runtime,
	// tránh query DB trên từng critical request. Đây là session snapshot, không
	// phải source-of-truth dài hạn của admin_devices table.
	DevicePublicKey string `json:"device_public_key,omitempty"`
	TokenJTI        string `json:"token_jti"`
	Version         int64  `json:"version"`
	LastSeenAt      int64  `json:"last_seen_at"`
	// last_seen_* được track realtime ở Redis runtime.
	// LastSeenDirty=true nghĩa là có delta mới so với snapshot trước đó
	// và cần flush xuống DB ở thời điểm session kết thúc/finalize.
	LastSeenIP        string `json:"last_seen_ip,omitempty"`
	LastSeenUserAgent string `json:"last_seen_user_agent,omitempty"`
	LastSeenDirty     bool   `json:"last_seen_dirty,omitempty"`
}

type adminDeviceRuntimeCache struct {
	rdb *goredis.Client
}

func NewAdminDeviceRuntimeCache(rdb *goredis.Client) AdminDeviceRuntimeCache {
	return &adminDeviceRuntimeCache{rdb: rdb}
}

func (c *adminDeviceRuntimeCache) SetDeviceRuntime(ctx context.Context, runtime AdminDeviceRuntime, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return fmt.Errorf("iam cache: ttl must be positive")
	}
	runtime.DeviceID = strings.TrimSpace(runtime.DeviceID)
	runtime.DeviceSecretHash = strings.TrimSpace(runtime.DeviceSecretHash)
	if runtime.DeviceID == "" || runtime.DeviceSecretHash == "" {
		return fmt.Errorf("iam cache: device runtime is invalid")
	}
	if runtime.Version <= 0 {
		runtime.Version = 1
	}
	payload, err := json.Marshal(runtime)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, c.key(runtime.DeviceID), payload, ttl).Err()
}

func (c *adminDeviceRuntimeCache) VerifyDeviceSecret(ctx context.Context, deviceID string, rawDeviceSecret string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, fmt.Errorf("iam cache: redis client is required")
	}
	record, err := c.GetDeviceRuntime(ctx, deviceID)
	if err != nil {
		return false, err
	}
	if record == nil {
		return false, nil
	}
	hashed := record.DeviceSecretHash
	if strings.TrimSpace(hashed) == "" {
		return false, nil
	}
	return strings.TrimSpace(hashed) == security.HashTokenSHA256(rawDeviceSecret), nil
}

func (c *adminDeviceRuntimeCache) GetDeviceRuntime(ctx context.Context, deviceID string) (*AdminDeviceRuntime, error) {
	if c == nil || c.rdb == nil {
		return nil, fmt.Errorf("iam cache: redis client is required")
	}
	raw, err := c.rdb.Get(ctx, c.key(deviceID)).Result()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record := AdminDeviceRuntime{}
	if unmarshalErr := json.Unmarshal([]byte(raw), &record); unmarshalErr == nil && strings.TrimSpace(record.DeviceSecretHash) != "" {
		return &record, nil
	}
	return nil, fmt.Errorf("iam cache: invalid device runtime record")
}

func (c *adminDeviceRuntimeCache) DeleteDeviceSecret(ctx context.Context, deviceID string) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}
	return c.rdb.Del(ctx, c.key(deviceID)).Err()
}

func (c *adminDeviceRuntimeCache) TouchDeviceSecret(ctx context.Context, deviceID string, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return fmt.Errorf("iam cache: ttl must be positive")
	}
	return c.rdb.Expire(ctx, c.key(deviceID), ttl).Err()
}

func (c *adminDeviceRuntimeCache) CompareAndTouchDeviceRuntime(ctx context.Context, deviceID string, expectedVersion int64, ttl time.Duration, ip *string, userAgent *string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return false, fmt.Errorf("iam cache: ttl must be positive")
	}
	if expectedVersion <= 0 {
		return false, fmt.Errorf("iam cache: expected version must be positive")
	}
	key := c.key(deviceID)
	ipValue := ""
	if ip != nil {
		ipValue = strings.TrimSpace(*ip)
	}
	uaValue := ""
	if userAgent != nil {
		uaValue = strings.TrimSpace(*userAgent)
	}
	lua := `
local raw = redis.call('GET', KEYS[1])
if not raw then
  return 0
end
local obj = cjson.decode(raw)
local current = tonumber(obj.version or 0)
if current ~= tonumber(ARGV[1]) then
  return 0
end
obj.version = current + 1
obj.last_seen_at = tonumber(ARGV[3])
-- mark dirty chỉ khi IP/UA thật sự thay đổi để giảm write xuống DB
local newIp = ARGV[4]
local newUA = ARGV[5]
if newIp ~= '' and tostring(obj.last_seen_ip or '') ~= newIp then
  obj.last_seen_ip = newIp
  obj.last_seen_dirty = true
end
if newUA ~= '' and tostring(obj.last_seen_user_agent or '') ~= newUA then
  obj.last_seen_user_agent = newUA
  obj.last_seen_dirty = true
end
local payload = cjson.encode(obj)
redis.call('SET', KEYS[1], payload, 'EX', tonumber(ARGV[2]))
return 1
`
	result, err := c.rdb.Eval(ctx, lua, []string{key}, expectedVersion, int(ttl.Seconds()), time.Now().UTC().Unix(), ipValue, uaValue).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (c *adminDeviceRuntimeCache) ScanDeviceRuntimes(ctx context.Context, limit int) ([]AdminDeviceRuntime, error) {
	if c == nil || c.rdb == nil {
		return nil, fmt.Errorf("iam cache: redis client is required")
	}
	if limit <= 0 {
		limit = 100
	}
	keys := make([]string, 0, limit)
	var cursor uint64
	pattern := c.key("*")
	for len(keys) < limit {
		batch, next, err := c.rdb.Scan(ctx, cursor, pattern, int64(limit)).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range batch {
			keys = append(keys, key)
			if len(keys) >= limit {
				break
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	out := make([]AdminDeviceRuntime, 0, len(keys))
	for _, key := range keys {
		raw, err := c.rdb.Get(ctx, key).Result()
		if err != nil {
			continue
		}
		record := AdminDeviceRuntime{}
		if json.Unmarshal([]byte(raw), &record) != nil || strings.TrimSpace(record.DeviceID) == "" {
			continue
		}
		out = append(out, record)
	}
	return out, nil
}

func (c *adminDeviceRuntimeCache) key(deviceID string) string {
	return "iam:admin:device:runtime:" + strings.TrimSpace(deviceID)
}
