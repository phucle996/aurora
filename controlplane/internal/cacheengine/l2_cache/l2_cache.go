/*
================================================================================
COMPONENT CONTRACT: L2 Cache (Redis KV Component)
================================================================================
Mục tiêu: Cung cấp giao thức đọc/ghi dữ liệu phân tán dùng chung trên L2 Redis.
Source of Truth (SoT): Dữ liệu phân tán được lưu trữ tại Redis Cluster.
Ranh giới (Boundary):
- Đọc/ghi payload nhị phân ([]byte) và số phiên bản (version) của dữ liệu.
- Hoàn toàn tách biệt khỏi L1. Caller tự điều phối mối liên kết L1 -> L2 -> DB.

Sơ đồ vị trí thành phần (Graph Representation):
[Callsite / Service nghiệp vụ] ──(Orchestrates)──> [L2Cache Interface]
                                                           │
                                             (Pipelined GET key:data & key:version)
                                                           │
                                                           ▼
                                               [L2 Storage (Redis Node)]

Quy tắc bất biến (Invariants):
1. Keyname Alignment: Key truyền từ Callsite phải đồng bộ hoàn toàn với L1 keyname.
2. Hash Tag Cluster Slot Protection: Tự động bọc keyname gốc bằng cặp ngoặc nhọn
   {keyname}:data và {keyname}:version để tránh lỗi CROSSSLOT trên Redis Cluster.
3. Pipeline Optimization: Đọc cả khóa dữ liệu và khóa phiên bản trong một lượt
   Pipeline duy nhất để giảm thiểu Round Trip Time (RTT).
================================================================================
*/

package l2_cache

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// L2Cache định nghĩa giao thức giao tiếp độc lập với tầng L2 Redis KV.
type L2Cache interface {
	// Get lấy dữ liệu payload thô và số phiên bản của khóa.
	// Trả về exists = false nếu khóa không tồn tại, không trả về lỗi redis.Nil.
	Get(ctx context.Context, key string) (payload []byte, version int64, exists bool, err error)
	// Set lưu trữ dữ liệu (sẽ được tự động Marshal sang JSON) và số phiên bản của khóa với TTL xác định.
	Set(ctx context.Context, key string, data interface{}, version int64, ttl time.Duration) error
	// Delete xóa hoàn toàn khóa dữ liệu và phiên bản trên L2.
	Delete(ctx context.Context, key string) error
	// Client trả về redis client bên dưới để thực thi các lệnh đặc thù.
	Client() *redis.Client
	// GetOrLoad đọc dữ liệu từ cache L2, nếu không tồn tại sẽ gọi loadFn để tải lại và lưu vào L2.
	GetOrLoad(ctx context.Context, key string, target interface{}, ttl time.Duration, loadFn func() (interface{}, error)) (version int64, err error)
}

// redisL2Cache triển khai L2Cache sử dụng go-redis client.
type redisL2Cache struct {
	rdb *redis.Client
}

// NewL2Cache khởi tạo một L2 cache Redis mới.
func NewL2Cache(rdb *redis.Client) L2Cache {
	return &redisL2Cache{rdb: rdb}
}

// Client trả về redis client bên dưới.
func (c *redisL2Cache) Client() *redis.Client {
	return c.rdb
}

// Get thực hiện truy vấn đồng thời data và version của khóa qua Redis Pipeline.
func (c *redisL2Cache) Get(ctx context.Context, key string) (payload []byte, version int64, exists bool, err error) {
	// 1. Đồng bộ keyname với L1 và bọc bằng Hash Tag để chỉ định cùng một Hash Slot
	dataKey := "{" + key + "}:data"
	versionKey := "{" + key + "}:version"

	// 2. Sử dụng Pipeline để gộp lệnh đọc thành 1 RTT duy nhất
	pipe := c.rdb.Pipeline()
	dataCmd := pipe.Get(ctx, dataKey)
	versionCmd := pipe.Get(ctx, versionKey)

	// Thực thi pipeline trên Redis
	_, err = pipe.Exec(ctx)
	if err != nil {
		// 3. Nếu xảy ra lỗi, kiểm tra xem có phải lỗi redis.Nil (key không tồn tại) không
		dataErr := dataCmd.Err()
		versionErr := versionCmd.Err()

		// Nếu 1 trong 2 key bị khuyết -> Coi như cache miss (exists = false)
		if dataErr == redis.Nil || versionErr == redis.Nil {
			return nil, 0, false, nil
		}
		// Trả về lỗi kết nối thực tế khác nếu có
		if dataErr != nil {
			return nil, 0, false, dataErr
		}
		if versionErr != nil {
			return nil, 0, false, versionErr
		}
		return nil, 0, false, err
	}

	// 4. Lấy kết quả từ pipeline thành công
	dataVal, _ := dataCmd.Result()
	versionVal, _ := versionCmd.Result()

	// Parse số phiên bản ngược về int64
	v, err := strconv.ParseInt(versionVal, 10, 64)
	if err != nil {
		return nil, 0, false, err
	}

	return []byte(dataVal), v, true, nil
}

// Set thực hiện ghi đồng thời data và version của khóa qua Redis Pipeline với TTL.
func (c *redisL2Cache) Set(ctx context.Context, key string, data interface{}, version int64, ttl time.Duration) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}

	dataKey := "{" + key + "}:data"
	versionKey := "{" + key + "}:version"

	pipe := c.rdb.Pipeline()
	pipe.Set(ctx, dataKey, payload, ttl)
	pipe.Set(ctx, versionKey, strconv.FormatInt(version, 10), ttl)

	_, err = pipe.Exec(ctx)
	return err
}

// Delete thực hiện xóa đồng thời cả data key và version key trên Redis.
func (c *redisL2Cache) Delete(ctx context.Context, key string) error {
	dataKey := "{" + key + "}:data"
	versionKey := "{" + key + "}:version"

	// Gọi lệnh xóa đa khóa (DEL) trên Redis
	_, err := c.rdb.Del(ctx, dataKey, versionKey).Result()
	return err
}

// GetOrLoad thực hiện đọc dữ liệu từ cache L2 Redis. Nếu cache miss, gọi loadFn để nạp và lưu vào Redis.
func (c *redisL2Cache) GetOrLoad(ctx context.Context, key string, target interface{}, ttl time.Duration, loadFn func() (interface{}, error)) (version int64, err error) {
	// 1. Kiểm tra cache L2 trước
	payload, v, exists, err := c.Get(ctx, key)
	if err != nil {
		return 0, err
	}
	if exists {
		// Unmarshal payload vào target
		if err := json.Unmarshal(payload, target); err != nil {
			return 0, err
		}
		return v, nil
	}

	// 2. Cache miss -> Nạp dữ liệu gốc qua loadFn
	val, err := loadFn()
	if err != nil {
		return 0, err
	}

	// Tự động sinh monotonic version dựa trên thời gian nạp thực tế
	newVersion := time.Now().UnixNano()

	// 3. Ghi đè vào L2 Cache
	if err := c.Set(ctx, key, val, newVersion, ttl); err != nil {
		return 0, err
	}

	// 4. Đồng bộ dữ liệu mới nạp vào target để trả về caller
	payloadVal, err := json.Marshal(val)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(payloadVal, target); err != nil {
		return 0, err
	}

	return newVersion, nil
}

