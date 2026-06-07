package cacheengine

import (
	"context"
	"fmt"
	"time"
)

// LoaderFunc định nghĩa hàm nạp dữ liệu gốc tĩnh từ Database/Services
type LoaderFunc func(ctx context.Context, param string) (interface{}, error)

// RegisteredLoader lưu thông tin cấu hình tĩnh cho từng nhóm dữ liệu cần cache
type RegisteredLoader struct {
	Namespace string
	TTL       time.Duration
	Load      LoaderFunc
	// Factory sinh ra đối tượng con trỏ kiểu (*T) phục vụ json.Unmarshal khi nhận được fanout payload
	Factory   func() interface{}
}

// CacheRegistry quản lý tập trung các loader tĩnh và làm việc trực tiếp với L1 Cache
type CacheRegistry struct {
	cache   Cache
	loaders map[string]*RegisteredLoader
}

// NewCacheRegistry khởi tạo một CacheRegistry mới
func NewCacheRegistry(cache Cache) *CacheRegistry {
	return &CacheRegistry{
		cache:   cache,
		loaders: make(map[string]*RegisteredLoader),
	}
}

// Register sử dụng Go Generics để tự động lấy kiểu dữ liệu T của loadFn và sinh hàm Factory tương ứng.
// Giúp lập trình viên chỉ cần truyền key, ttl, và loadFn mà không cần truyền hàm sinh struct trống thủ công.
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

// InvalidateLocal thực hiện xóa cache trực tiếp trên RAM L1 của instance hiện tại.
// Hàm này hoàn toàn tách biệt với Redis Fanout (không tự kích hoạt publish).
func (r *CacheRegistry) InvalidateLocal(ctx context.Context, key string) bool {
	return r.cache.Delete(key)
}

// GetLocalRaw lấy dữ liệu envelope thô trực tiếp từ RAM, không chạy loader
func (r *CacheRegistry) GetLocalRaw(key string) (interface{}, bool) {
	return r.cache.Get(key)
}

// SetLocalRaw ghi đè trực tiếp dữ liệu thô vào RAM L1
func (r *CacheRegistry) SetLocalRaw(key string, val interface{}, ttl time.Duration) {
	r.cache.Set(key, val, ttl)
}

// GetLoader truy xuất thông tin loader đăng ký theo namespace
func (r *CacheRegistry) GetLoader(namespace string) *RegisteredLoader {
	return r.loaders[namespace]
}

// GetCache trả về instance Cache L1 bên dưới (phục vụ test hoặc liên kết ngoài)
func (r *CacheRegistry) GetCache() Cache {
	return r.cache
}

// Flush xóa sạch toàn bộ L1 cache bên dưới
func (r *CacheRegistry) Flush() {
	r.cache.Flush()
}

// Close dừng dọn dẹp các tài nguyên chạy nền của Cache L1
func (r *CacheRegistry) Close() {
	r.cache.Close()
}

// GetOrLoad đọc dữ liệu cache từ namespace tương ứng thông qua tham số đầu vào.
// Phương thức này tự động bọc (wrap) dữ liệu thô vào CacheEnvelope và sinh monotonic version dựa trên time.Now().UnixNano().
// Sau đó, nó tự giải nén để trả về trực tiếp dữ liệu gốc (Value) cho caller, che giấu Envelope nội bộ.
func (r *CacheRegistry) GetOrLoad(ctx context.Context, namespace string, param string) (interface{}, error) {
	loader, ok := r.loaders[namespace]
	if !ok {
		return nil, fmt.Errorf("cacheengine: namespace '%s' is not registered", namespace)
	}

	// Tạo cache key độc nhất kết hợp giữa namespace và tham số
	cacheKey := namespace
	if param != "" {
		cacheKey = fmt.Sprintf("%s:%s", namespace, param)
	}

	envelopeVal, err := r.cache.GetOrLoad(cacheKey, loader.TTL, func() (interface{}, error) {
		// Gọi loader của caller để nạp dữ liệu gốc từ DB/Service
		raw, err := loader.Load(ctx, param)
		if err != nil {
			return nil, err
		}

		// Tự động sinh monotonic version dựa trên thời gian nạp thực tế
		version := time.Now().UnixNano()

		// Đóng gói thô trực tiếp vào CacheEnvelope (Zero-Serialization)
		return &CacheEnvelope{
			Key:     cacheKey,
			Version: version,
			Value:   raw,
		}, nil
	})
	if err != nil {
		return nil, err
	}

	// Ép kiểu ngược lại từ CacheEnvelope để trả về dữ liệu Value gốc cho caller
	envelope, ok := envelopeVal.(*CacheEnvelope)
	if !ok {
		return nil, fmt.Errorf("cacheengine: internal error, invalid cache item type in registry")
	}

	return envelope.Value, nil
}
