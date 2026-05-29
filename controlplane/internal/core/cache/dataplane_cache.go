// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/cache/dataplane_cache.go
//            Đặc Tả Hạ Tầng Dynamic State & Transient Cache Của Cụm Dataplane Cluster
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & TỐI ƯU HÓA TRẠNG THÁI ĐỘNG (CONTRACT & CACHE DESIGN OPTIMIZATION):
//   - Chịu trách nhiệm quản lý toàn bộ các trạng thái tạm thời (Transient State), bao gồm các khóa
//     bảo chứng sự sống (heartbeats/leases) và metrics hiệu năng của Dataplane Cluster trên Redis.
//   - Tối ưu hóa hiệu năng truy xuất cực hạn (High-Throughput / Ultra-Low Latency):
//
//     1) FAST PATH HEARTBEAT LEASE TRACKING:
//        * Sử dụng Redis Keys `core:dataplane:lease:{zone_id}` với TTL cực ngắn (8 giây) để làm thước đo
//          sống sót liveness O(1) tốc độ cực cao, giảm tải 99% I/O cho PostgreSQL.
//
//     2) STRUCT-BASED MEMORY CACHING:
//        * Quản lý metrics động của cụm dưới dạng JSON thô tối ưu hóa quá trình serialize.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Redis Cache là nguồn tin cậy duy nhất (Transient Source of Truth) cho trạng thái sống động (liveness)
//     của cụm Dataplane Cluster trong vòng 8 giây.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Đóng gói toàn bộ driver go-redis, che giấu các chi tiết lệnh Redis (Set, Exists, Get) khỏi các tầng trên.
//   - Không tự ý sinh key động lộn xộn, mọi key pattern phải được quản lý tập trung và an toàn.
//
// 🔄 CALLSITE FLOW:
//   - Được inject trực tiếp vào `DataplaneNodeService` và `DataplaneOrchestrator` để phục vụ
//     Fast Probing và Background Safety Sweep.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Đảm bảo Redis Client được cấu hình Connection Pool và Read/Write Timeout chặt chẽ tránh blocking.
//
// ======================================================================================================

package coreCache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type DataplaneCache interface {
	// AcquireLease ghi nhận hoặc gia hạn khóa bảo chứng sự sống (lease key) của cụm Dataplane theo Zone.
	AcquireLease(ctx context.Context, zoneID string, ttl time.Duration) error

	// CheckLeaseExists kiểm tra xem cụm Dataplane của Zone đó còn lease hoạt động trên Redis không (O(1)).
	CheckLeaseExists(ctx context.Context, zoneID string) (bool, error)

	// SaveClusterMetrics lưu trữ metrics động (tài nguyên, queue depth...) của cụm Dataplane.
	SaveClusterMetrics(ctx context.Context, clusterID string, metrics map[string]interface{}, ttl time.Duration) error

	// GetClusterMetrics đọc metrics động của cụm Dataplane phục vụ cho việc cân bằng tải.
	GetClusterMetrics(ctx context.Context, clusterID string) (map[string]interface{}, error)

	// Subscribe đăng ký lắng nghe tin nhắn từ Redis Pub/Sub channel.
	Subscribe(ctx context.Context, channel string) *goredis.PubSub
}

type DataplaneCacheImpl struct {
	rdb *goredis.Client
}

// Subscribe thực hiện subscribe vào một channel cụ thể trên Redis.
func (c *DataplaneCacheImpl) Subscribe(ctx context.Context, channel string) *goredis.PubSub {
	return c.rdb.Subscribe(ctx, channel)
}

// NewDataplaneCacheImpl khởi tạo thực thể Cache triển khai cho Dataplane.
func NewDataplaneCacheImpl(rdb *goredis.Client) DataplaneCache {
	return &DataplaneCacheImpl{rdb: rdb}
}

// AcquireLease thực hiện ghi nhận sự sống của Zone lên Redis bằng Set-Key với TTL xác định.
func (c *DataplaneCacheImpl) AcquireLease(ctx context.Context, zoneID string, ttl time.Duration) error {
	// Step 1: Tạo Redis Key định danh duy nhất theo zone_id.
	key := fmt.Sprintf("core:dataplane:lease:%s", zoneID)

	// Step 2: Set key với giá trị "1" cùng TTL chỉ định để Redis tự động hủy nếu mất nhịp heartbeat.
	return c.rdb.Set(ctx, key, "1", ttl).Err()
}

// CheckLeaseExists kiểm tra O(1) xem key lease có tồn tại trên Redis không.
func (c *DataplaneCacheImpl) CheckLeaseExists(ctx context.Context, zoneID string) (bool, error) {
	// Step 1: Xây dựng Redis Key tương ứng với zone_id.
	key := fmt.Sprintf("core:dataplane:lease:%s", zoneID)

	// Step 2: Gọi lệnh EXISTS của Redis để kiểm tra nhanh.
	count, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}

	// Step 3: Trả về true nếu số lượng key tìm thấy lớn hơn 0.
	return count > 0, nil
}

// SaveClusterMetrics ghi nhận cấu hình metrics chi tiết dạng JSON vào Redis.
func (c *DataplaneCacheImpl) SaveClusterMetrics(ctx context.Context, clusterID string, metrics map[string]interface{}, ttl time.Duration) error {
	// Step 1: Xây dựng Redis Key theo cluster_id.
	key := fmt.Sprintf("core:dataplane:metrics:%s", clusterID)

	// Step 2: Marshal map metrics sang chuỗi JSON bytes.
	payload, err := json.Marshal(metrics)
	if err != nil {
		return err
	}

	// Step 3: Lưu vào Redis với TTL động.
	return c.rdb.Set(ctx, key, payload, ttl).Err()
}

// GetClusterMetrics đọc chuỗi JSON metrics từ Redis và unmarshal ngược lại.
func (c *DataplaneCacheImpl) GetClusterMetrics(ctx context.Context, clusterID string) (map[string]interface{}, error) {
	// Step 1: Xây dựng Redis Key.
	key := fmt.Sprintf("core:dataplane:metrics:%s", clusterID)

	// Step 2: Lấy dữ liệu từ Redis.
	val, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		// Step 3: Nếu key không tồn tại, trả về nil/nil an toàn.
		if err == goredis.Nil {
			return nil, nil
		}
		return nil, err
	}

	// Step 4: Parse JSON ngược lại thành map để trả về.
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(val), &out); err != nil {
		return nil, err
	}
	return out, nil
}
