package cacheengine

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"controlplane/internal/cacheengine/codec"
	"controlplane/internal/cacheengine/fanout_cache"
	"controlplane/internal/cacheengine/l1_cache"
	"controlplane/internal/cacheengine/l2_cache"
	"controlplane/internal/cacheengine/l2_lua_executor"
	"controlplane/pkg/logger"

	"github.com/redis/go-redis/v9"
)

// ============================================================================
// TYPE ALIASES (EXPOSE SUB-PACKAGE INTERFACES TO ROOT API SURFACE)
// ============================================================================
type Cache = l1_cache.Cache
type L1Envelope = l1_cache.L1Envelope
type RedisFanout = fanout_cache.RedisFanout
type L2Cache = l2_cache.L2Cache
type L2LuaExecutor = l2_lua_executor.L2LuaExecutor
type L2Envelope = l2_cache.L2Envelope

// ============================================================================
// CONSTRUCTORS (MODULE PATTERN FOR ISOLATED CREATION)
// ============================================================================

// NewL1Cache khởi tạo một in-memory L1 cache phân mảnh định dạng interface
func NewL1Cache() Cache {
	return l1_cache.NewShardedCache()
}

// NewShardedCache khởi tạo một in-memory L1 cache phân mảnh định dạng interface (Alias tương thích ngược)
func NewShardedCache() Cache {
	return NewL1Cache()
}

// NewL2Cache khởi tạo một Redis-based L2 cache định dạng interface
func NewL2Cache(rdb *redis.Client) L2Cache {
	return l2_cache.NewL2Cache(rdb)
}

// NewL2LuaExecutor khởi tạo một Lua script runner nguyên tử định dạng interface
func NewL2LuaExecutor(rdb *redis.Client) L2LuaExecutor {
	return l2_lua_executor.NewL2LuaExecutor(rdb)
}

// NewRedisFanout khởi tạo một Redis Pub/Sub bus đồng bộ định dạng struct cụ thể
func NewRedisFanout(rdb *redis.Client, channel string) *RedisFanout {
	return fanout_cache.NewRedisFanout(rdb, channel)
}

// LoaderFunc định nghĩa hàm nạp dữ liệu gốc tĩnh từ Database/Services
type LoaderFunc func(ctx context.Context, param string) (interface{}, error)

// RegisteredLoader lưu thông tin cấu hình tĩnh cho từng nhóm dữ liệu cần cache
type RegisteredLoader struct {
	Namespace string
	TTL       time.Duration
	Load      LoaderFunc
	// Factory sinh ra đối tượng con trỏ kiểu (*T) phục vụ json.Unmarshal khi nhận được fanout payload
	Factory func() interface{}
}

// CacheRegistry quản lý tập trung các loader tĩnh và điều phối L1, L2, Fanout, Executor
type CacheRegistry struct {
	L1      Cache
	L2      L2Cache
	Fanout  *RedisFanout
	Exec    L2LuaExecutor
	loaders map[string]*RegisteredLoader
}

// NewCacheRegistry khởi tạo một CacheRegistry mới với L1 Cache được bao bọc bởi telemetry decorator.
func NewCacheRegistry(l1 Cache) *CacheRegistry {
	return &CacheRegistry{
		L1:      &telemetryL1Cache{raw: l1},
		loaders: make(map[string]*RegisteredLoader),
	}
}

// Register sử dụng Go Generics để tự động lấy kiểu dữ liệu T của loadFn và sinh hàm Factory tương ứng.
func Register[T any](
	r *CacheRegistry,
	namespace string,
	ttl time.Duration,
	loadFn func(ctx context.Context, param string) (T, error),
) {
	r.loaders[namespace] = &RegisteredLoader{
		Namespace: namespace,
		TTL:       ttl,
		Load: func(ctx context.Context, param string) (interface{}, error) {
			return loadFn(ctx, param)
		},
		Factory: func() interface{} {
			var zero T
			return &zero // Trả về con trỏ *T để truyền vào json.Unmarshal
		},
	}
}

// GetLoader truy xuất thông tin loader đăng ký theo namespace
func (r *CacheRegistry) GetLoader(namespace string) *RegisteredLoader {
	return r.loaders[namespace]
}

// StartSubscribe kết nối loop đồng bộ với Fanout sub-package, tự động tiêm các callback xử lý
func (r *CacheRegistry) StartSubscribe(ctx context.Context) error {
	if r.Fanout == nil {
		return fmt.Errorf("cacheengine: fanout bus is not configured")
	}
	r.Fanout.SetCallbacks(r.handleFanoutMessage, r.L1.Flush)
	return r.Fanout.StartSubscribe(ctx)
}

// handleFanoutMessage xử lý tin nhắn đồng bộ từ Pub/Sub: dọn dẹp hoặc cập nhật L1 RAM an toàn
func (r *CacheRegistry) handleFanoutMessage(key string, payload []byte, version int64) {
	// 1. Trường hợp xóa cache (Delete Invalidation)
	if len(payload) == 0 {
		r.L1.Delete(key)
		return
	}

	// 2. Trường hợp cập nhật cache (Update)
	// SRE Warning: Tránh lỗi OOM bằng cách kiểm tra key có đang tồn tại trong RAM L1 cục bộ không.
	val, exists := r.L1.Get(key)
	if !exists {
		return
	}

	// Kiểm tra phiên bản đơn điệu để tránh việc tin nhắn đến trễ đè lên dữ liệu mới hơn (stale write)
	localEnv, ok := val.(*L1Envelope)
	if !ok || version <= localEnv.Version {
		return
	}

	// Tách chuỗi key để xác định namespace (ví dụ: "zone_catalog:dynamic" -> "zone_catalog")
	parts := strings.SplitN(key, ":", 2)
	if len(parts) == 0 {
		return
	}
	namespace := parts[0]

	loader := r.GetLoader(namespace)
	if loader == nil || loader.Factory == nil {
		return
	}

	// Tạo instance trống thông qua Factory tự động của registry
	ptrTarget := loader.Factory()
	if err := codec.UnmarshalData(payload, ptrTarget); err != nil {
		return
	}

	// Giải tham chiếu con trỏ thô để lấy struct/slice nguyên bản
	rawVal := reflect.ValueOf(ptrTarget).Elem().Interface()

	// Cập nhật trực tiếp vào L1 Cache cục bộ
	r.L1.Set(key, &L1Envelope{
		Key:     key,
		Version: version,
		Value:   rawVal,
	}, loader.TTL)
}

// GetOrLoad đọc dữ liệu cache từ namespace tương ứng thông qua tham số đầu vào.
func (r *CacheRegistry) GetOrLoad(ctx context.Context, namespace string, param string) (interface{}, error) {
	loader, ok := r.loaders[namespace]
	if !ok {
		return nil, fmt.Errorf("cacheengine: namespace '%s' is not registered", namespace)
	}

	cacheKey := namespace
	if param != "" {
		cacheKey = fmt.Sprintf("%s:%s", namespace, param)
	}

	envelopeVal, err := r.L1.GetOrLoad(cacheKey, loader.TTL, func() (interface{}, error) {
		// Log thông tin khi gặp Cache Miss trong RAM L1 để tiện theo dõi và debug luồng dữ liệu
		logger.SysInfoFields("cache.get_or_load", "L1 cache miss, triggering loader callback", logger.Fields{
			"key": cacheKey,
		})

		// Gọi loader của caller để nạp dữ liệu gốc từ DB/Service
		raw, err := loader.Load(ctx, param)
		if err != nil {
			// Log lỗi khi hàm loader bị lỗi (ví dụ lỗi kết nối DB, DB query error) và trả lỗi gốc về
			logger.SysErrorFields("cache.get_or_load", "L1 loader execution failed, returning database/service error", err, logger.Fields{
				"key": cacheKey,
			})
			return nil, err
		}

		// Tự động sinh monotonic version dựa trên thời gian nạp thực tế
		version := time.Now().UnixNano()

		// Đóng gói thô trực tiếp vào L1Envelope (Zero-Serialization)
		return &L1Envelope{
			Key:     cacheKey,
			Version: version,
			Value:   raw,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	// Ép kiểu ngược lại từ L1Envelope để trả về dữ liệu Value gốc cho caller
	envelope, ok := envelopeVal.(*L1Envelope)
	if !ok {
		return nil, fmt.Errorf("cacheengine: internal error, invalid cache item type in registry")
	}

	return envelope.Value, nil
}
