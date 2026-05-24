package iamCache

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// UserDeviceCapLock cung cấp advisory lock ngắn hạn theo user cho luồng
// đăng nhập/quản lý giới hạn thiết bị đồng thời.
//
// CONTRACT:
// - Lock key: `iam:user:device:cap_lock:<user_id>`.
// - `TryAcquire` trả về owner token khi key chưa tồn tại (SET NX + TTL).
// - `Release` chỉ xoá lock key khi token khớp owner hiện tại.
//
// BOUNDARY:
// - Chỉ là coordination primitive để chống xử lý đồng thời đụng cap logic.
// - Không phải session SoT, không thay thế transaction/lock ở DB.
// - Caller phải tự quyết định fail-open/fail-closed nếu lock/cache lỗi.
//
// CALLSITE/TÁC DỤNG:
// - Dùng ở auth service khi nhiều login cùng user chạy song song.
// - Mục tiêu: dồn cạnh tranh về 1 worker để tránh race evict-cap (BR-009).
type UserDeviceCapLock interface {
	TryAcquire(ctx context.Context, userID string, ttl time.Duration) (token string, acquired bool, err error)
	Release(ctx context.Context, userID string, token string) error
}

type userDeviceCapLock struct {
	rdb *goredis.Client
}

// NewUserDeviceCapLock khởi tạo Redis-backed cap lock cho user-device flow.
func NewUserDeviceCapLock(rdb *goredis.Client) UserDeviceCapLock {
	return &userDeviceCapLock{rdb: rdb}
}

// TryAcquire thử lấy lock theo user trong TTL chỉ định.
// Redis primitive: SET key value NX EX ttl.
// Return:
// - ok=true: caller trở thành owner lock ở thời điểm hiện tại.
// - ok=false: lock đã có owner khác (caller nên retry/backoff hoặc skip).
func (c *userDeviceCapLock) TryAcquire(ctx context.Context, userID string, ttl time.Duration) (string, bool, error) {
	if c == nil || c.rdb == nil {
		return "", false, fmt.Errorf("iam cache: redis client is required")
	}
	if ttl <= 0 {
		return "", false, fmt.Errorf("iam cache: ttl must be positive")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return "", false, fmt.Errorf("iam cache: user id is required")
	}
	ownerToken := uuid.NewString()
	ok, err := c.rdb.SetNX(ctx, c.key(userID), ownerToken, ttl).Result()
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	return ownerToken, true, nil
}

// Release giải phóng lock theo user.
// Redis primitive: DEL key.
// Lưu ý: lock này không dùng owner token, nên caller phải bảo đảm đúng scope
// sử dụng để tránh xoá lock của luồng không liên quan.
func (c *userDeviceCapLock) Release(ctx context.Context, userID string, token string) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("iam cache: user id is required")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("iam cache: lock token is required")
	}
	lua := `
local v = redis.call('GET', KEYS[1])
if not v then
  return 0
end
if v ~= ARGV[1] then
  return 0
end
redis.call('DEL', KEYS[1])
return 1
`
	_, err := c.rdb.Eval(ctx, lua, []string{c.key(userID)}, token).Int()
	return err
}

// key trả về lock key chuẩn cho user device cap flow.
func (c *userDeviceCapLock) key(userID string) string {
	return "iam:user:device:cap_lock:" + strings.TrimSpace(userID)
}
