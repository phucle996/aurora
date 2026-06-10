package iamCache

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"

	goredis "github.com/redis/go-redis/v9"
)

// UserDeviceRuntimeCache là SoT runtime cho presence và device-binding của user
// session. Runtime key chính theo cặp (user_id, access_key).
//
// CONTRACT:
// - Key chính: `iam:user:device:runtime:<user_id>:<access_key>`.
// - Auth-path verify runtime phải lookup O(1) theo key chính, không scan list.
// - `tracked_device_id` trong record là định danh bridge sang DB device.
// - Rotate runtime phải atomic để tránh drift current/previous jti và key move.
//
// BOUNDARY:
// - Package này chỉ quản lý runtime state trong Redis.
// - Business policy (reject/pass/fail-closed) thuộc middleware/service caller.
// - Không thay thế repository DB và không quyết định auth semantics.
//
// OPERATIONS NOTE:
//   - `ScanByUser` chỉ cho presence/list flows; không dùng thay runtime identity
//     verification trên hot auth-path.
type UserDeviceRuntimeCache interface {
	SetDeviceRuntime(ctx context.Context, runtime UserDeviceRuntime, ttl time.Duration) error
	GetDeviceRuntimeByUserDevice(ctx context.Context, userID, accessKey string) (*UserDeviceRuntime, error)
	DeleteDeviceRuntimeByUserDevice(ctx context.Context, userID, accessKey string) error
	RotateFragmentForUserDevice(ctx context.Context, userID, accessKey, expectedJTI, newAccessKey, newAccessSecretHash, newJTI string, ttl time.Duration, ip *string, userAgent *string) (bool, error)
	TouchDeviceRuntimeByUserDevice(ctx context.Context, userID, accessKey string, ttl time.Duration, ip *string, userAgent *string) (bool, error)
	ScanByUser(ctx context.Context, userID string, limit int) ([]UserDeviceRuntime, error)
}

// UserDeviceRuntime là payload runtime stored ở Redis.
//
// FIELD CONTRACT (trace nhanh theo flow):
//   - Nhóm identity runtime: UserID + AccessKey
//     -> xác định duy nhất runtime key `iam:user:device:runtime:<user_id>:<access_key>`.
//   - Nhóm auth-fragment: AccessSecretHash + CurrentJTI (+ PreviousJTI/PreviousIssuedAt)
//     -> dùng để verify request hiện tại và grace-window ngay sau rotate.
//   - Nhóm bridge DB: TrackedDeviceID
//     -> liên kết runtime session với thiết bị persistent trong DB.
//   - Nhóm presence/ops: Status, LastSeen*, Version
//     -> phục vụ online state, telemetry, và quan sát thay đổi runtime.
type UserDeviceRuntime struct {
	// AccessKey là access_key ngắn hạn (fragment ID) nằm trong JWT claim
	// và cookie access_key. Đây là phần "device" trong key runtime Redis.
	AccessKey string `json:"access_key"`
	// AccessSecretHash là SHA-256 của cookie access_secret (không lưu raw secret).
	// Access verify dùng field này để so khớp fragment secret an toàn.
	AccessSecretHash string `json:"access_secret_hash"`
	// CurrentJTI là jti của access token hiện hành đang có hiệu lực cho runtime.
	// Mismatch CurrentJTI (ngoài grace) sẽ bị reject.
	CurrentJTI string `json:"current_jti"`
	// PreviousJTI là jti trước đó ngay sau lần rotate gần nhất.
	// Chỉ dùng cho grace-window ngắn để giảm reject do race request đồng thời.
	PreviousJTI string `json:"previous_jti,omitempty"`
	// PreviousIssuedAt là thời điểm phát hành của PreviousJTI, dùng để tính grace.
	PreviousIssuedAt int64 `json:"previous_issued_at,omitempty"`
	// CurrentIssuedAt là thời điểm phát hành/rotate của CurrentJTI.
	CurrentIssuedAt int64 `json:"current_issued_at,omitempty"`
	// TrackedDeviceID là device id persistent trong DB (`iam.devices.id`).
	// Dùng cho list/revoke/logout-others theo thực thể thiết bị lâu dài.
	TrackedDeviceID string `json:"tracked_device_id"`
	// UserID là chủ sở hữu session runtime; kết hợp với AccessKey tạo identity key.
	UserID string `json:"user_id"`
	// Status phản ánh trạng thái runtime phục vụ presence (online/revoked...).
	// Access match hiện reject khi status == revoked.
	Status string `json:"status,omitempty"`
	// Version tăng dần theo mutate runtime (rotate/touch) để hỗ trợ audit/debug.
	Version int64 `json:"version"`
	// LastSeenAt là Unix timestamp lần cuối runtime được xác nhận hoạt động.
	LastSeenAt int64 `json:"last_seen_at"`
	// LastSeenIP lưu IP quan sát gần nhất (phục vụ risk/audit/hiển thị).
	LastSeenIP string `json:"last_seen_ip,omitempty"`
	// LastSeenUserAgent lưu UA gần nhất (phục vụ risk/audit/hiển thị).
	LastSeenUserAgent string `json:"last_seen_user_agent,omitempty"`
	// LastSeenDirty đánh dấu metadata last_seen thay đổi để downstream xử lý batch.
	LastSeenDirty bool `json:"last_seen_dirty,omitempty"`
}

type userDeviceRuntimeCache struct {
	rdb *goredis.Client
}

// NewUserDeviceRuntimeCache returns the redis-backed cache implementation.
func NewUserDeviceRuntimeCache(rdb *goredis.Client) UserDeviceRuntimeCache {
	return &userDeviceRuntimeCache{rdb: rdb}
}

// SetDeviceRuntime upsert runtime record + đồng bộ user index access_key.
// Redis primitive: SET + SADD + EXPIRE (pipeline).
// Callsite chính: login/refresh/auth services khi ghi runtime session.
// Tác dụng: đảm bảo runtime SoT và index liệt kê theo user luôn cùng trạng thái.
func (c *userDeviceRuntimeCache) SetDeviceRuntime(ctx context.Context, runtime UserDeviceRuntime, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return fmt.Errorf("iam cache: ttl must be positive")
	}
	runtime.AccessKey = strings.TrimSpace(runtime.AccessKey)
	runtime.AccessSecretHash = strings.TrimSpace(runtime.AccessSecretHash)
	runtime.CurrentJTI = strings.TrimSpace(runtime.CurrentJTI)
	runtime.UserID = strings.TrimSpace(runtime.UserID)
	runtime.TrackedDeviceID = strings.TrimSpace(runtime.TrackedDeviceID)
	if runtime.AccessKey == "" || runtime.AccessSecretHash == "" ||
		runtime.CurrentJTI == "" || runtime.UserID == "" || runtime.TrackedDeviceID == "" {
		return iamTaxonomy.ErrUserDeviceRuntimeInvalid
	}
	if runtime.Version <= 0 {
		runtime.Version = 1
	}
	if runtime.LastSeenAt <= 0 {
		runtime.LastSeenAt = time.Now().UTC().Unix()
	}
	if runtime.CurrentIssuedAt <= 0 {
		runtime.CurrentIssuedAt = runtime.LastSeenAt
	}
	if runtime.Status == "" {
		runtime.Status = "online"
	}
	payload, err := json.Marshal(runtime)
	if err != nil {
		return err
	}
	pipe := c.rdb.TxPipeline()
	pipe.Set(ctx, c.userDeviceKey(runtime.UserID, runtime.AccessKey), payload, ttl)
	pipe.SAdd(ctx, c.userDeviceIndexKey(runtime.UserID), runtime.AccessKey)
	pipe.Expire(ctx, c.userDeviceIndexKey(runtime.UserID), ttl+24*time.Hour)
	if _, execErr := pipe.Exec(ctx); execErr != nil {
		return execErr
	}
	return nil
}

// GetDeviceRuntimeByUserDevice đọc runtime theo key chính
// `iam:user:device:runtime:<user_id>:<access_key>`.
// Redis primitive: GET + JSON unmarshal.
func (c *userDeviceRuntimeCache) GetDeviceRuntimeByUserDevice(ctx context.Context, userID, accessKey string) (*UserDeviceRuntime, error) {
	if c == nil || c.rdb == nil {
		return nil, fmt.Errorf("iam cache: redis client is required")
	}
	userID = strings.TrimSpace(userID)
	accessKey = strings.TrimSpace(accessKey)
	if userID == "" || accessKey == "" {
		return nil, iamTaxonomy.ErrUserDeviceRuntimeInvalid
	}
	raw, err := c.rdb.Get(ctx, c.userDeviceKey(userID, accessKey)).Result()
	if err == goredis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record := UserDeviceRuntime{}
	if jsonErr := json.Unmarshal([]byte(raw), &record); jsonErr != nil {
		return nil, fmt.Errorf("iam cache: invalid user device runtime payload: %w", jsonErr)
	}
	if strings.TrimSpace(record.AccessSecretHash) == "" || strings.TrimSpace(record.AccessKey) == "" {
		return nil, iamTaxonomy.ErrUserDeviceRuntimeInvalid
	}
	return &record, nil
}

// RotateFragmentForUserDevice rotate atomically fragment + jti cho 1 runtime.
// Redis primitive: EVAL Lua (GET old -> mutate -> SET new -> SADD/SREM index -> DEL old).
func (c *userDeviceRuntimeCache) RotateFragmentForUserDevice(ctx context.Context, userID, accessKey, expectedJTI, newAccessKey, newAccessSecretHash, newJTI string, ttl time.Duration, ip *string, userAgent *string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return false, fmt.Errorf("iam cache: ttl must be positive")
	}
	userID = strings.TrimSpace(userID)
	accessKey = strings.TrimSpace(accessKey)
	newAccessKey = strings.TrimSpace(newAccessKey)
	newAccessSecretHash = strings.TrimSpace(newAccessSecretHash)
	newJTI = strings.TrimSpace(newJTI)
	if userID == "" || accessKey == "" || newAccessKey == "" || newAccessSecretHash == "" || newJTI == "" {
		return false, iamTaxonomy.ErrUserDeviceRuntimeInvalid
	}
	lua := `
local raw = redis.call('GET', KEYS[1])
if not raw then
  return 0
end
local obj = cjson.decode(raw)
local expected = ARGV[1]
if expected ~= '' and tostring(obj.current_jti or '') ~= expected then
  return 0
end
obj.previous_jti = tostring(obj.current_jti or '')
obj.previous_issued_at = tonumber(obj.current_issued_at or obj.last_seen_at or 0)
obj.current_jti = ARGV[2]
obj.current_issued_at = tonumber(ARGV[5])
obj.last_seen_at = tonumber(ARGV[5])
obj.access_key = ARGV[3]
obj.access_secret_hash = ARGV[4]
obj.version = tonumber(obj.version or 0) + 1
local payload = cjson.encode(obj)
redis.call('SET', KEYS[2], payload, 'EX', tonumber(ARGV[6]))
redis.call('SADD', KEYS[3], ARGV[3])
redis.call('EXPIRE', KEYS[3], tonumber(ARGV[7]))
if KEYS[1] ~= KEYS[2] then
  redis.call('DEL', KEYS[1])
  redis.call('SREM', KEYS[3], ARGV[8])
end
return 1
`
	now := time.Now().UTC().Unix()
	indexTTL := int((ttl + 24*time.Hour).Seconds())
	result, err := c.rdb.Eval(ctx, lua, []string{c.userDeviceKey(userID, accessKey), c.userDeviceKey(userID, newAccessKey), c.userDeviceIndexKey(userID)},
		strings.TrimSpace(expectedJTI),
		newJTI,
		newAccessKey,
		newAccessSecretHash,
		now,
		int(ttl.Seconds()),
		indexTTL,
		accessKey,
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// TouchDeviceRuntimeByUserDevice cập nhật last_seen* + TTL theo cách atomic.
// Redis primitive: EVAL Lua (GET -> update last_seen/ip/ua -> SET EX).
func (c *userDeviceRuntimeCache) TouchDeviceRuntimeByUserDevice(ctx context.Context, userID, accessKey string, ttl time.Duration, ip *string, userAgent *string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return false, fmt.Errorf("iam cache: ttl must be positive")
	}
	userID = strings.TrimSpace(userID)
	accessKey = strings.TrimSpace(accessKey)
	if userID == "" || accessKey == "" {
		return false, iamTaxonomy.ErrUserDeviceRuntimeInvalid
	}
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
obj.last_seen_at = tonumber(ARGV[1])
local newIp = ARGV[2]
local newUA = ARGV[3]
if newIp ~= '' and tostring(obj.last_seen_ip or '') ~= newIp then
  obj.last_seen_ip = newIp
  obj.last_seen_dirty = true
end
if newUA ~= '' and tostring(obj.last_seen_user_agent or '') ~= newUA then
  obj.last_seen_user_agent = newUA
  obj.last_seen_dirty = true
end
local payload = cjson.encode(obj)
redis.call('SET', KEYS[1], payload, 'EX', tonumber(ARGV[4]))
return 1
`
	result, err := c.rdb.Eval(ctx, lua, []string{c.userDeviceKey(userID, accessKey)},
		time.Now().UTC().Unix(),
		ipValue,
		uaValue,
		int(ttl.Seconds()),
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

// DeleteDeviceRuntimeByUserDevice xóa runtime key và gỡ access_key khỏi user index.
// Redis primitive: DEL + SREM (pipeline).
func (c *userDeviceRuntimeCache) DeleteDeviceRuntimeByUserDevice(ctx context.Context, userID, accessKey string) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}
	userID = strings.TrimSpace(userID)
	accessKey = strings.TrimSpace(accessKey)
	if userID == "" || accessKey == "" {
		return iamTaxonomy.ErrUserDeviceRuntimeInvalid
	}
	pipe := c.rdb.TxPipeline()
	pipe.Del(ctx, c.userDeviceKey(userID, accessKey))
	pipe.SRem(ctx, c.userDeviceIndexKey(userID), accessKey)
	_, execErr := pipe.Exec(ctx)
	return execErr
}

// ScanByUser liệt kê runtime records của 1 user thông qua secondary index.
// Redis primitive: SSCAN index -> MGET runtime keys.
func (c *userDeviceRuntimeCache) ScanByUser(ctx context.Context, userID string, limit int) ([]UserDeviceRuntime, error) {
	if c == nil || c.rdb == nil {
		return nil, fmt.Errorf("iam cache: redis client is required")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, iamTaxonomy.ErrUserDeviceRuntimeInvalid
	}
	if limit <= 0 {
		limit = 100
	}
	keys := make([]string, 0, limit)
	var cursor uint64
	for len(keys) < limit {
		scanned, nextCursor, scanErr := c.rdb.SScan(ctx, c.userDeviceIndexKey(userID), cursor, "*", int64(limit)).Result()
		if scanErr != nil {
			return nil, scanErr
		}
		for _, accessKey := range scanned {
			keys = append(keys, c.userDeviceKey(userID, accessKey))
			if len(keys) >= limit {
				break
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	if len(keys) == 0 {
		return []UserDeviceRuntime{}, nil
	}
	values, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]UserDeviceRuntime, 0, len(values))
	for _, raw := range values {
		if raw == nil {
			continue
		}
		rawStr, ok := raw.(string)
		if !ok {
			continue
		}
		record := UserDeviceRuntime{}
		if jsonErr := json.Unmarshal([]byte(rawStr), &record); jsonErr != nil {
			return nil, fmt.Errorf("iam cache: invalid user device runtime payload: %w", jsonErr)
		}
		if record.UserID != userID {
			return nil, iamTaxonomy.ErrUserDeviceRuntimeInvalid
		}
		out = append(out, record)
	}
	return out, nil
}

func (c *userDeviceRuntimeCache) userDeviceKey(userID, accessKey string) string {
	return "iam:user:device:runtime:" + strings.TrimSpace(userID) + ":" + strings.TrimSpace(accessKey)
}

func (c *userDeviceRuntimeCache) userDeviceIndexKey(userID string) string {
	return "iam:user:device:index:" + strings.TrimSpace(userID)
}

func MatchRuntime(record *UserDeviceRuntime, accessKey, rawAccessSecret, jti string, graceWindow time.Duration) bool {
	if record == nil {
		return false
	}
	if strings.TrimSpace(accessKey) == "" || strings.TrimSpace(rawAccessSecret) == "" || strings.TrimSpace(jti) == "" {
		return false
	}
	if record.Status == "revoked" {
		return false
	}
	if record.AccessKey != accessKey {
		return false
	}
	if record.AccessSecretHash != security.HashTokenSHA256(rawAccessSecret) {
		return false
	}
	if record.CurrentJTI == jti {
		return true
	}
	if graceWindow > 0 && record.PreviousJTI != "" && record.PreviousJTI == jti {
		issuedAt := record.PreviousIssuedAt
		if issuedAt > 0 && time.Since(time.Unix(issuedAt, 0)) <= graceWindow {
			return true
		}
	}
	return false
}
