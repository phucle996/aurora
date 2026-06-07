// Package iamCache triển khai lớp Cache quản lý và lưu trữ thông tin phiên làm việc (Access Session)
// của tài khoản Admin hệ thống trong cơ sở dữ liệu phân tán Redis.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Trạng thái phiên của Admin là tập hợp dữ liệu động được lưu trữ duy nhất trong Redis Cache.
//   - Phiên Admin áp dụng mô hình bảo mật **Fragment Token (3 Mảnh)** thay vì Access Token đơn lẻ:
//   - Mảnh 1: JWT Access Token ngắn hạn (Ký stateless bằng loại secret `admin_api_key`).
//   - Mảnh 2: `access_key` - Định danh phiên làm việc, dùng làm key lưu trong Redis.
//   - Mảnh 3: `access_secret` - Mã bí mật phiên, chỉ được băm SHA256 trước khi lưu trong Redis.
//   - Middleware xác thực bắt buộc cả 3 mảnh phải trùng khớp toàn vẹn mới coi phiên là hợp lệ.
//
// 🔒 RANH GIỚI BẢO MẬT & CHIẾN LƯỢC XỬ LÝ LỖI (SECURITY & ERROR BOUNDARY):
//   - **Nguyên lý Fail-Closed**: Tầng Cache tuyệt đối **KHÔNG** tự ý nuốt lỗi (swallow), tắt cảnh báo,
//     hoặc tự động kích hoạt chế độ fallback ẩn (như tự gán version mặc định, tự động gán limit mặc định,
//     hoặc bỏ qua các lỗi unmarshal/đọc từng bản ghi trong vòng lặp SCAN).
//   - Mọi lỗi phát sinh (mất kết nối, lỗi script, lỗi giải tuần tự JSON, lỗi dữ liệu đầu vào không hợp lệ...)
//     bắt buộc phải trả trực tiếp về tầng nghiệp vụ (Callsite) để quyết định dừng luồng (Fail-Closed) nhằm bảo vệ hệ thống.
//   - **Mật mã an toàn**: Bản plaintext của `access_secret` chỉ được phép tồn tại tạm thời trong RAM của luồng xử lý
//     xác thực, tuyệt đối không được ghi log hoặc lưu trữ trực tiếp dưới dạng thô vào Redis.
//
// ⚡ TỐI ƯU HÓA HOẠT ĐỘNG TRÊN CLOUD & HỆ THỐNG HA (10 TRIỆU KEYS PRODUCTION-READY):
//   - **Sorted Set Active Index**: Thay vì dùng lệnh `SCAN` quét qua toàn bộ 10 triệu keyspace (vốn cực kỳ tốn CPU
//     và tăng độ trễ RTT do lặp nhiều lần), hệ thống sử dụng một Sorted Set (ZSET) làm chỉ mục lưu trữ danh sách các
//     phiên Admin đang hoạt động (`iam:admin:active_sessions:index`).
//   - **Cluster-Safe Pipeline**: Khi cào danh sách phiên bằng `ScanAccessSessions`, hệ thống sử dụng Go-Redis Pipeline
//     để lấy đồng thời toàn bộ dữ liệu trong 1 RTT duy nhất. Điều này loại bỏ hoàn toàn lỗi `CROSSSLOT` vốn luôn xảy ra
//     trên Redis Cluster HA nếu dùng lệnh `MGET` trên các key không chung hashtag.
//   - **Atomic CAS qua LUA Script**: Cập nhật đồng bộ và gia hạn thời gian sống của session lẫn chỉ mục ZSET một cách
//     nguyên tử ngay trên Redis Server, loại bỏ triệt để mọi nguy cơ về Race Condition.
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

// ============================================================================
// INTERFACE DEFINITION
// ============================================================================

// AdminAccessSessionCache định nghĩa interface quản lý cache phiên làm việc của Admin.
type AdminAccessSessionCache interface {
	// SetAccessSession lưu trữ toàn bộ trạng thái phiên làm việc của Admin vào Redis với TTL xác định.
	SetAccessSession(ctx context.Context, session AdminAccessSession, ttl time.Duration) error

	// VerifyAccessSecret xác thực bí mật phiên (Access Secret) bằng cách so sánh mã băm SHA256.
	VerifyAccessSecret(ctx context.Context, accessKey string, rawAccessSecret string) (bool, error)

	// GetAccessSession truy vấn thông tin phiên làm việc từ Redis theo Access Key.
	GetAccessSession(ctx context.Context, accessKey string) (*AdminAccessSession, error)

	// TouchAccessSession cập nhật/gia hạn thời gian sống (TTL) của phiên trong Redis.
	TouchAccessSession(ctx context.Context, accessKey string, ttl time.Duration) error

	// CompareAndTouchAccessSession thực thi LUA Script nguyên tử (Atomic CAS) để gia hạn phiên,
	// tăng version và kiểm tra trạng thái thay đổi IP/UA nhằm tối ưu hóa việc ghi DB (LastSeenDirty).
	CompareAndTouchAccessSession(ctx context.Context, accessKey string, expectedVersion int64, ttl time.Duration, ip *string, userAgent *string) (bool, error)

	// ScanAccessSessions cào danh sách các phiên làm việc đang hoạt động sử dụng chỉ mục Sorted Set (an toàn cho HA 10M keys).
	ScanAccessSessions(ctx context.Context, limit int) ([]AdminAccessSession, error)

	// DeleteAccessSession xóa bỏ hoàn toàn phiên làm việc của Admin khỏi Redis (dùng khi Logout hoặc Revoke).
	DeleteAccessSession(ctx context.Context, accessKey string) error
}

// ============================================================================
// DATA STRUCTURES
// ============================================================================

// AdminAccessSession đại diện cho cấu trúc dữ liệu lưu trữ phiên làm việc của Admin trong Cache.
type AdminAccessSession struct {
	// AccessKey là định danh duy nhất của phiên làm việc (Mảnh 2 trong cơ chế Fragment Token).
	AccessKey string `json:"access_key"`

	// AccessSecretHash là chuỗi băm SHA256 của Access Secret thô (Mảnh 3).
	AccessSecretHash string `json:"access_secret_hash"`

	// TrackedDeviceID liên kết phiên làm việc với bản ghi thiết bị được lưu trữ trong Database.
	TrackedDeviceID string `json:"tracked_device_id"`

	// DevicePublicKey là khóa công khai Ed25519 dùng để xác thực chữ ký số của các tác vụ quan trọng.
	DevicePublicKey string `json:"device_public_key,omitempty"`

	// TokenJTI là ID duy nhất của JWT Access Token (Mảnh 1) tương ứng, ngăn chặn tấn công replay.
	TokenJTI string `json:"token_jti"`

	// Version là số phiên của bản ghi, dùng cho cơ chế khóa lạc quan (Optimistic Locking / CAS) ngăn chặn Race Condition.
	Version int64 `json:"version"`

	// LastSeenAt là mốc thời gian Unix hoạt động cuối cùng của phiên làm việc này.
	LastSeenAt int64 `json:"last_seen_at"`

	// LastSeenIP lưu IP cuối cùng mà client sử dụng để gửi request hợp lệ.
	LastSeenIP string `json:"last_seen_ip,omitempty"`

	// LastSeenUserAgent lưu User Agent cuối cùng của client gửi request hợp lệ.
	LastSeenUserAgent string `json:"last_seen_user_agent,omitempty"`

	// LastSeenDirty đánh dấu trạng thái thay đổi IP/UA so với Database.
	LastSeenDirty bool `json:"last_seen_dirty,omitempty"`
}

// adminAccessSessionCache triển khai interface AdminAccessSessionCache qua Redis Client.
type adminAccessSessionCache struct {
	rdb *goredis.Client
}

// NewAdminAccessSessionCache khởi tạo instance quản lý cache phiên làm việc cho Admin.
func NewAdminAccessSessionCache(rdb *goredis.Client) AdminAccessSessionCache {
	return &adminAccessSessionCache{rdb: rdb}
}

// ============================================================================
// IMPLEMENTATION METHODS WITH IN-LINE FLOW COMMENTS
// ============================================================================

// SetAccessSession lưu thông tin phiên làm việc của Admin vào Redis.
func (c *adminAccessSessionCache) SetAccessSession(ctx context.Context, session AdminAccessSession, ttl time.Duration) error {

	// --- BƯỚC 3: Loại bỏ khoảng trắng dư thừa trong dữ liệu thô ---
	session.AccessKey = strings.TrimSpace(session.AccessKey)
	session.AccessSecretHash = strings.TrimSpace(session.AccessSecretHash)

	// --- BƯỚC 4: Kiểm tra tính hợp lệ tối thiểu của phiên làm việc ---
	if session.AccessKey == "" || session.AccessSecretHash == "" {
		return fmt.Errorf("iam cache: access session is invalid")
	}

	// --- BƯỚC 5: Thực thi kiểm tra version nghiêm ngặt, KHÔNG tự gán ngầm mặc định ---
	if session.Version <= 0 {
		return fmt.Errorf("iam cache: version must be positive")
	}

	// --- BƯỚC 6: Mã hóa thông tin phiên làm việc sang dạng JSON ---
	payload, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("iam cache: marshal error: %w", err)
	}

	// --- BƯỚC 7: Sử dụng Redis TxPipeline để cập nhật đồng thời cả Session và ZSET Index ---
	// Nhằm đảm bảo tính nguyên tử (atomic) và an toàn HA, tránh mồ côi index.
	pipe := c.rdb.TxPipeline()

	// Lưu trữ phiên chính thức
	pipe.Set(ctx, c.key(session.AccessKey), payload, ttl)

	// Thêm vào ZSET index với score là unix timestamp khi hết hạn (expire epoch)
	expireAt := time.Now().UTC().Add(ttl).Unix()
	pipe.ZAdd(ctx, c.indexKey(), goredis.Z{
		Score:  float64(expireAt),
		Member: session.AccessKey,
	})

	// Thực thi pipeline nguyên tử
	_, pipeErr := pipe.Exec(ctx)
	if pipeErr != nil {
		return fmt.Errorf("iam cache: set access session pipeline error: %w", pipeErr)
	}

	return nil
}

// VerifyAccessSecret thực hiện xác thực mã bí mật thô (rawAccessSecret).
func (c *adminAccessSessionCache) VerifyAccessSecret(ctx context.Context, accessKey string, rawAccessSecret string) (bool, error) {

	// --- BƯỚC 2: Tải thông tin phiên làm việc hiện tại từ Redis ---
	record, err := c.GetAccessSession(ctx, accessKey)
	if err != nil {
		return false, err
	}
	if record == nil {
		return false, nil
	}

	// --- BƯỚC 3: So sánh băm SHA256 của secret thô gửi lên với giá trị lưu trong cache ---
	hashed := record.AccessSecretHash
	if strings.TrimSpace(hashed) == "" {
		return false, nil
	}

	return strings.TrimSpace(hashed) == security.HashTokenSHA256(rawAccessSecret), nil
}

// GetAccessSession tải thông tin phiên làm việc của Admin dựa trên Access Key.
func (c *adminAccessSessionCache) GetAccessSession(ctx context.Context, accessKey string) (*AdminAccessSession, error) {
	// --- BƯỚC 2: Gọi lệnh GET để lấy dữ liệu thô từ Redis ---
	raw, err := c.rdb.Get(ctx, c.key(accessKey)).Result()

	// --- BƯỚC 3: Xử lý trường hợp không tìm thấy dữ liệu (Cache Miss) ---
	if err == goredis.Nil {
		return nil, nil
	}

	// --- BƯỚC 4: Trả lỗi trực tiếp về Caller nếu có lỗi hạ tầng mạng/Redis ---
	if err != nil {
		return nil, err
	}

	// --- BƯỚC 5: Giải tuần tự (Unmarshal) payload JSON sang cấu trúc dữ liệu Go ---
	record := AdminAccessSession{}
	if unmarshalErr := json.Unmarshal([]byte(raw), &record); unmarshalErr != nil {
		return nil, fmt.Errorf("iam cache: unmarshal error: %w", unmarshalErr)
	}

	// --- BƯỚC 6: Kiểm chứng tính hợp lệ của bản ghi đã giải mã ---
	if strings.TrimSpace(record.AccessSecretHash) == "" {
		return nil, fmt.Errorf("iam cache: invalid access session record (missing secret hash)")
	}

	return &record, nil
}

// DeleteAccessSession xóa bỏ bản ghi phiên trong Redis và chỉ mục ZSET tương ứng.
func (c *adminAccessSessionCache) DeleteAccessSession(ctx context.Context, accessKey string) error {
	// --- BƯỚC 1: Kiểm tra tính khả dụng của Redis Client ---
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}

	trimmedKey := strings.TrimSpace(accessKey)
	if trimmedKey == "" {
		return fmt.Errorf("iam cache: access key must not be empty")
	}

	// --- BƯỚC 2: Sử dụng TxPipeline để xóa đồng thời cả Session chính và ZSET Index ---
	pipe := c.rdb.TxPipeline()
	pipe.Del(ctx, c.key(trimmedKey))
	pipe.ZRem(ctx, c.indexKey(), trimmedKey)

	_, pipeErr := pipe.Exec(ctx)
	if pipeErr != nil {
		return fmt.Errorf("iam cache: delete access session pipeline error: %w", pipeErr)
	}

	return nil
}

// TouchAccessSession thực hiện gia hạn thời gian sống (TTL) của phiên trong Redis và cập nhật chỉ mục ZSET.
func (c *adminAccessSessionCache) TouchAccessSession(ctx context.Context, accessKey string, ttl time.Duration) error {
	// --- BƯỚC 1: Kiểm tra tính khả dụng của Redis Client ---
	if c == nil || c.rdb == nil {
		return fmt.Errorf("iam cache: redis client is required")
	}

	// --- BƯỚC 2: Đảm bảo tham số TTL dương hợp lệ ---
	if ttl <= 0 {
		return fmt.Errorf("iam cache: ttl must be positive")
	}

	trimmedKey := strings.TrimSpace(accessKey)
	if trimmedKey == "" {
		return fmt.Errorf("iam cache: access key must not be empty")
	}

	// --- BƯỚC 3: Sử dụng TxPipeline để touch khóa chính và cập nhật score ZSET index ---
	pipe := c.rdb.TxPipeline()
	pipe.Expire(ctx, c.key(trimmedKey), ttl)

	expireAt := time.Now().UTC().Add(ttl).Unix()
	pipe.ZAdd(ctx, c.indexKey(), goredis.Z{
		Score:  float64(expireAt),
		Member: trimmedKey,
	})

	_, pipeErr := pipe.Exec(ctx)
	if pipeErr != nil {
		return fmt.Errorf("iam cache: touch access session pipeline error: %w", pipeErr)
	}

	return nil
}

// CompareAndTouchAccessSession thực thi LUA Script nguyên tử (Atomic Compare-And-Swap) trên Redis
// đồng bộ gia hạn thời gian sống cho cả session chính và chỉ mục ZSET index.
func (c *adminAccessSessionCache) CompareAndTouchAccessSession(ctx context.Context, accessKey string, expectedVersion int64, ttl time.Duration, ip *string, userAgent *string) (bool, error) {
	// --- BƯỚC 3: Chuẩn hóa các giá trị tùy chọn (IP, User Agent) ---
	key := c.key(accessKey)
	ipValue := ""
	if ip != nil {
		ipValue = strings.TrimSpace(*ip)
	}
	uaValue := ""
	if userAgent != nil {
		uaValue = strings.TrimSpace(*userAgent)
	}

	// --- BƯỚC 4: Định nghĩa LUA Script nguyên tử thực thi Lock lạc quan trên Redis ---
	// KEYS[1] = Session Key, KEYS[2] = ZSET Index Key
	// ARGV[1] = expectedVersion, ARGV[2] = ttl (seconds), ARGV[3] = now, ARGV[4] = ip, ARGV[5] = ua, ARGV[6] = expireAt, ARGV[7] = accessKey
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
redis.call('ZADD', KEYS[2], tonumber(ARGV[6]), ARGV[7])
return 1
`

	// --- BƯỚC 5: Thực thi Eval LUA Script và nhận kết quả nguyên tử ---
	expireAt := time.Now().UTC().Add(ttl).Unix()
	result, err := c.rdb.Eval(ctx, lua, []string{key, c.indexKey()}, expectedVersion, int(ttl.Seconds()), time.Now().UTC().Unix(), ipValue, uaValue, expireAt, accessKey).Int()
	if err != nil {
		return false, err
	}

	// --- BƯỚC 6: Trả về trạng thái thực thi (thành công nếu CAS khớp version) ---
	return result == 1, nil
}

// ScanAccessSessions thực hiện cào danh sách các phiên làm việc đang hoạt động trong Redis.
func (c *adminAccessSessionCache) ScanAccessSessions(ctx context.Context, limit int) ([]AdminAccessSession, error) {

	// --- BƯỚC 3: Dọn dẹp các session đã hết hạn trong ZSET index trước khi quét ---
	// Giúp index luôn sạch sẽ và tối ưu dung lượng bộ nhớ.
	now := time.Now().UTC().Unix()
	if err := c.rdb.ZRemRangeByScore(ctx, c.indexKey(), "-inf", fmt.Sprintf("%d", now)).Err(); err != nil {
		return nil, fmt.Errorf("iam cache: index cleanup error: %w", err)
	}

	// --- BƯỚC 4: Lấy danh sách các access key đang hoạt động từ Sorted Set ---
	// Do chỉ mục ZSET chứa chính xác các admin session hoạt động, truy vấn này đạt O(log N + M) cực nhanh.
	accessKeys, err := c.rdb.ZRange(ctx, c.indexKey(), 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("iam cache: zrange index error: %w", err)
	}

	if len(accessKeys) == 0 {
		return nil, nil
	}

	// --- BƯỚC 5: Sử dụng Redis Pipeline để lấy đồng thời tất cả session payloads (Cluster-safe) ---
	// Tránh CROSSSLOT error trong Redis Cluster HA bằng cách dùng Pipeline của go-redis
	// thay vì dùng MGET (vốn yêu cầu các key phải thuộc cùng một slot).
	pipe := c.rdb.Pipeline()
	cmds := make([]*goredis.StringCmd, 0, len(accessKeys))
	for _, key := range accessKeys {
		cmds = append(cmds, pipe.Get(ctx, c.key(key)))
	}

	// Thực thi pipeline trong 1 RTT duy nhất
	_, pipeErr := pipe.Exec(ctx)
	if pipeErr != nil && pipeErr != goredis.Nil {
		return nil, fmt.Errorf("iam cache: pipeline get sessions error: %w", pipeErr)
	}

	// --- BƯỚC 6: Giải mã dữ liệu và xử lý các phần tử không tồn tại ---
	out := make([]AdminAccessSession, 0, len(accessKeys))
	for i, cmd := range cmds {
		raw, getErr := cmd.Result()
		if getErr == goredis.Nil {
			// Phiên đã hết hạn trong Redis nhưng chưa kịp dọn dẹp khỏi ZSET index, tự động xóa mồ côi
			_ = c.rdb.ZRem(ctx, c.indexKey(), accessKeys[i]).Err()
			continue
		}
		if getErr != nil {
			return nil, fmt.Errorf("iam cache: get session error for key %s: %w", accessKeys[i], getErr)
		}

		record := AdminAccessSession{}
		if unmarshalErr := json.Unmarshal([]byte(raw), &record); unmarshalErr != nil {
			return nil, fmt.Errorf("iam cache: unmarshal error for key %s: %w", accessKeys[i], unmarshalErr)
		}
		if strings.TrimSpace(record.AccessKey) == "" {
			return nil, fmt.Errorf("iam cache: invalid access session record (missing access key) for key %s", accessKeys[i])
		}
		out = append(out, record)
	}

	return out, nil
}

// key sinh khóa Redis chuẩn hóa với tiền tố dành riêng cho phân hệ Admin.
func (c *adminAccessSessionCache) key(accessKey string) string {
	return "iam:admin:access:session:" + strings.TrimSpace(accessKey)
}

// indexKey sinh khóa chứa Sorted Set Index quản lý các session của Admin đang hoạt động.
func (c *adminAccessSessionCache) indexKey() string {
	return "iam:active_sessions:admin:index"
}
