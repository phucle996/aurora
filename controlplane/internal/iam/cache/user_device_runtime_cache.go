package iamCache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/security"

	goredis "github.com/redis/go-redis/v9"
)

// UserDeviceRuntimeCache là SoT runtime cho presence và device-binding của user
// session. Mirror admin runtime: rotate fragment + jti per access token, giữ
// tracking_id ổn định cho 1 thiết bị / phiên dài hạn.
type UserDeviceRuntimeCache interface {
	SetDeviceRuntime(ctx context.Context, runtime UserDeviceRuntime, ttl time.Duration) error
	GetDeviceRuntime(ctx context.Context, trackingID string) (*UserDeviceRuntime, error)
	VerifyFragmentAndJTI(ctx context.Context, trackingID, deviceID, rawDeviceSecret, jti string, graceWindow time.Duration) (bool, error)
	RotateFragmentForJTI(ctx context.Context, trackingID, expectedJTI, newDeviceID, newDeviceSecretHash, newJTI string, ttl time.Duration, ip *string, userAgent *string) (bool, error)
	TouchDeviceRuntime(ctx context.Context, trackingID string, ttl time.Duration, ip *string, userAgent *string) (bool, error)
	DeleteDeviceRuntime(ctx context.Context, trackingID string) error
	ScanByUser(ctx context.Context, userID string, limit int) ([]UserDeviceRuntime, error)
}

// UserDeviceRuntime là payload runtime stored ở Redis.
type UserDeviceRuntime struct {
	TrackingID        string `json:"tracking_id"`
	DeviceID          string `json:"device_id"`
	DeviceSecretHash  string `json:"device_secret_hash"`
	CurrentJTI        string `json:"current_jti"`
	PreviousJTI       string `json:"previous_jti,omitempty"`
	PreviousIssuedAt  int64  `json:"previous_issued_at,omitempty"`
	CurrentIssuedAt   int64  `json:"current_issued_at,omitempty"`
	TrackedDeviceRef  string `json:"tracked_device_ref"`
	UserID            string `json:"user_id"`
	Status            string `json:"status,omitempty"`
	Version           int64  `json:"version"`
	LastSeenAt        int64  `json:"last_seen_at"`
	LastSeenIP        string `json:"last_seen_ip,omitempty"`
	LastSeenUserAgent string `json:"last_seen_user_agent,omitempty"`
	LastSeenDirty     bool   `json:"last_seen_dirty,omitempty"`
}

type userDeviceRuntimeCache struct {
	rdb *goredis.Client
}

// NewUserDeviceRuntimeCache returns the redis-backed cache implementation.
func NewUserDeviceRuntimeCache(rdb *goredis.Client) UserDeviceRuntimeCache {
	return &userDeviceRuntimeCache{rdb: rdb}
}

// ErrUserDeviceRuntimeInvalid is returned khi payload thiếu field bắt buộc.
var ErrUserDeviceRuntimeInvalid = errors.New("iam cache: invalid user device runtime")

func (c *userDeviceRuntimeCache) SetDeviceRuntime(ctx context.Context, runtime UserDeviceRuntime, ttl time.Duration) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return fmt.Errorf("iam cache: ttl must be positive")
	}
	runtime.TrackingID = strings.TrimSpace(runtime.TrackingID)
	runtime.DeviceID = strings.TrimSpace(runtime.DeviceID)
	runtime.DeviceSecretHash = strings.TrimSpace(runtime.DeviceSecretHash)
	runtime.CurrentJTI = strings.TrimSpace(runtime.CurrentJTI)
	runtime.UserID = strings.TrimSpace(runtime.UserID)
	runtime.TrackedDeviceRef = strings.TrimSpace(runtime.TrackedDeviceRef)
	if runtime.TrackingID == "" || runtime.DeviceID == "" || runtime.DeviceSecretHash == "" ||
		runtime.CurrentJTI == "" || runtime.UserID == "" || runtime.TrackedDeviceRef == "" {
		return ErrUserDeviceRuntimeInvalid
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
	pipe.Set(ctx, c.key(runtime.TrackingID), payload, ttl)
	pipe.SAdd(ctx, c.indexKey(runtime.UserID), runtime.TrackingID)
	if idxTTL := ttl + 24*time.Hour; idxTTL > 0 {
		pipe.Expire(ctx, c.indexKey(runtime.UserID), idxTTL)
	}
	if _, execErr := pipe.Exec(ctx); execErr != nil {
		return execErr
	}
	return nil
}

func (c *userDeviceRuntimeCache) GetDeviceRuntime(ctx context.Context, trackingID string) (*UserDeviceRuntime, error) {
	if c == nil || c.rdb == nil {
		return nil, fmt.Errorf("iam cache: redis client is required")
	}
	trackingID = strings.TrimSpace(trackingID)
	if trackingID == "" {
		return nil, ErrUserDeviceRuntimeInvalid
	}
	raw, err := c.rdb.Get(ctx, c.key(trackingID)).Result()
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
	if strings.TrimSpace(record.DeviceSecretHash) == "" || strings.TrimSpace(record.DeviceID) == "" {
		return nil, ErrUserDeviceRuntimeInvalid
	}
	return &record, nil
}

func (c *userDeviceRuntimeCache) VerifyFragmentAndJTI(ctx context.Context, trackingID, deviceID, rawDeviceSecret, jti string, graceWindow time.Duration) (bool, error) {
	record, err := c.GetDeviceRuntime(ctx, trackingID)
	if err != nil {
		return false, err
	}
	return matchRuntime(record, deviceID, rawDeviceSecret, jti, graceWindow), nil
}

func (c *userDeviceRuntimeCache) RotateFragmentForJTI(ctx context.Context, trackingID, expectedJTI, newDeviceID, newDeviceSecretHash, newJTI string, ttl time.Duration, ip *string, userAgent *string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return false, fmt.Errorf("iam cache: ttl must be positive")
	}
	if strings.TrimSpace(trackingID) == "" || strings.TrimSpace(newDeviceID) == "" ||
		strings.TrimSpace(newDeviceSecretHash) == "" || strings.TrimSpace(newJTI) == "" {
		return false, ErrUserDeviceRuntimeInvalid
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
if ARGV[1] ~= '' and tostring(obj.current_jti or '') ~= ARGV[1] then
  return 0
end
obj.previous_jti = tostring(obj.current_jti or '')
obj.previous_issued_at = tonumber(obj.current_issued_at or obj.last_seen_at or 0)
obj.current_jti = ARGV[2]
obj.current_issued_at = tonumber(ARGV[6])
obj.device_id = ARGV[3]
obj.device_secret_hash = ARGV[4]
obj.version = (tonumber(obj.version or 0) + 1)
obj.last_seen_at = tonumber(ARGV[6])
local newIp = ARGV[7]
local newUA = ARGV[8]
if newIp ~= '' and tostring(obj.last_seen_ip or '') ~= newIp then
  obj.last_seen_ip = newIp
  obj.last_seen_dirty = true
end
if newUA ~= '' and tostring(obj.last_seen_user_agent or '') ~= newUA then
  obj.last_seen_user_agent = newUA
  obj.last_seen_dirty = true
end
local payload = cjson.encode(obj)
redis.call('SET', KEYS[1], payload, 'EX', tonumber(ARGV[5]))
return 1
`
	result, err := c.rdb.Eval(ctx, lua, []string{c.key(trackingID)},
		strings.TrimSpace(expectedJTI),
		strings.TrimSpace(newJTI),
		strings.TrimSpace(newDeviceID),
		strings.TrimSpace(newDeviceSecretHash),
		int(ttl.Seconds()),
		time.Now().UTC().Unix(),
		ipValue,
		uaValue,
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (c *userDeviceRuntimeCache) TouchDeviceRuntime(ctx context.Context, trackingID string, ttl time.Duration, ip *string, userAgent *string) (bool, error) {
	if c == nil || c.rdb == nil {
		return false, fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return false, fmt.Errorf("iam cache: ttl must be positive")
	}
	trackingID = strings.TrimSpace(trackingID)
	if trackingID == "" {
		return false, ErrUserDeviceRuntimeInvalid
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
obj.last_seen_at = tonumber(ARGV[2])
local newIp = ARGV[3]
local newUA = ARGV[4]
if newIp ~= '' and tostring(obj.last_seen_ip or '') ~= newIp then
  obj.last_seen_ip = newIp
  obj.last_seen_dirty = true
end
if newUA ~= '' and tostring(obj.last_seen_user_agent or '') ~= newUA then
  obj.last_seen_user_agent = newUA
  obj.last_seen_dirty = true
end
local payload = cjson.encode(obj)
redis.call('SET', KEYS[1], payload, 'EX', tonumber(ARGV[1]))
return 1
`
	result, err := c.rdb.Eval(ctx, lua, []string{c.key(trackingID)},
		int(ttl.Seconds()),
		time.Now().UTC().Unix(),
		ipValue,
		uaValue,
	).Int()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (c *userDeviceRuntimeCache) DeleteDeviceRuntime(ctx context.Context, trackingID string) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}
	trackingID = strings.TrimSpace(trackingID)
	if trackingID == "" {
		return ErrUserDeviceRuntimeInvalid
	}
	record, _ := c.GetDeviceRuntime(ctx, trackingID)
	pipe := c.rdb.TxPipeline()
	pipe.Del(ctx, c.key(trackingID))
	if record != nil && strings.TrimSpace(record.UserID) != "" {
		pipe.SRem(ctx, c.indexKey(record.UserID), trackingID)
	}
	_, execErr := pipe.Exec(ctx)
	return execErr
}

func (c *userDeviceRuntimeCache) ScanByUser(ctx context.Context, userID string, limit int) ([]UserDeviceRuntime, error) {
	if c == nil || c.rdb == nil {
		return nil, fmt.Errorf("iam cache: redis client is required")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, ErrUserDeviceRuntimeInvalid
	}
	if limit <= 0 {
		limit = 100
	}
	members, err := c.rdb.SMembers(ctx, c.indexKey(userID)).Result()
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return []UserDeviceRuntime{}, nil
	}
	if len(members) > limit {
		members = members[:limit]
	}
	keys := make([]string, 0, len(members))
	for _, trackingID := range members {
		keys = append(keys, c.key(trackingID))
	}
	values, err := c.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	out := make([]UserDeviceRuntime, 0, len(values))
	stale := make([]string, 0)
	for i, raw := range values {
		if raw == nil {
			stale = append(stale, members[i])
			continue
		}
		rawStr, ok := raw.(string)
		if !ok {
			continue
		}
		record := UserDeviceRuntime{}
		if json.Unmarshal([]byte(rawStr), &record) != nil {
			continue
		}
		if record.UserID != userID {
			continue
		}
		out = append(out, record)
	}
	if len(stale) > 0 {
		_ = c.rdb.SRem(ctx, c.indexKey(userID), interfaceSlice(stale)...).Err()
	}
	return out, nil
}

func interfaceSlice(in []string) []interface{} {
	out := make([]interface{}, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}

func (c *userDeviceRuntimeCache) key(trackingID string) string {
	return "iam:user:device:runtime:" + strings.TrimSpace(trackingID)
}

func (c *userDeviceRuntimeCache) indexKey(userID string) string {
	return "iam:user:device:by_user:" + strings.TrimSpace(userID)
}

// MatchRuntime kiểm cặp (deviceID, rawDeviceSecret, jti) với record runtime đã
// load sẵn để middleware có thể chỉ tốn 1 Redis GET.
//
// Hành vi đồng nhất với VerifyFragmentAndJTI(record): grace window cho old jti.
func (UserDeviceRuntime) MatchRuntime(record *UserDeviceRuntime, deviceID, rawDeviceSecret, jti string, graceWindow time.Duration) bool {
	return matchRuntime(record, deviceID, rawDeviceSecret, jti, graceWindow)
}

func matchRuntime(record *UserDeviceRuntime, deviceID, rawDeviceSecret, jti string, graceWindow time.Duration) bool {
	if record == nil {
		return false
	}
	if strings.TrimSpace(deviceID) == "" || strings.TrimSpace(rawDeviceSecret) == "" || strings.TrimSpace(jti) == "" {
		return false
	}
	if record.Status == "revoked" {
		return false
	}
	if record.DeviceID != deviceID {
		return false
	}
	if record.DeviceSecretHash != security.HashTokenSHA256(rawDeviceSecret) {
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

// MatchUserDeviceRuntime expose helper match cho consumers ngoài package
// (vd middleware) khi đã load runtime sẵn để giảm round-trip.
func MatchUserDeviceRuntime(record *UserDeviceRuntime, deviceID, rawDeviceSecret, jti string, graceWindow time.Duration) bool {
	return matchRuntime(record, deviceID, rawDeviceSecret, jti, graceWindow)
}
