package app

import (
	"context"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	"controlplane/pkg/logger"
)

// InitL1CacheRegistry khởi tạo L1 Cache Registry trống ban đầu
func InitL1CacheRegistry() *cacheengine.CacheRegistry {
	l1Cache := cacheengine.NewShardedCache()
	return cacheengine.NewCacheRegistry(l1Cache)
}

// RegisterL1Loaders đăng ký toàn bộ các cache loaders sử dụng đồ thị phụ thuộc Modules hoàn chỉnh,
// và chạy goroutine lắng nghe sự kiện đồng bộ từ fanoutBus.
func RegisterL1Loaders(
	registry *cacheengine.CacheRegistry,
	modules *Modules,
	fanoutBus *cacheengine.RedisFanout,
) {
	// 2. Kích hoạt subscription loop chạy nền để xử lý tin nhắn đồng bộ từ các instance khác
	go func() {
		ctx := context.Background()
		if err := fanoutBus.StartSubscribe(ctx); err != nil {
			logger.SysWarn("cacheengine", "subscription loop terminated: "+err.Error())
		}
	}()

	// 3. Đăng ký tĩnh loader cho "zone_by_code" sử dụng generic function
	cacheengine.Register(registry, "zone_by_code", 5*time.Minute, func(ctx context.Context, param string) (string, error) {
		zoneID, err := modules.Core.ZoneRepository.GetZoneIDByCode(ctx, param)
		return zoneID.String(), err
	})

	// 4. Đăng ký tĩnh loader cho danh sách "zone_catalog" phục vụ dropdown/select UI
	cacheengine.Register(registry, "zone_catalog", 10*time.Minute, func(ctx context.Context, param string) ([]coreEntity.ZoneCatalog, error) {
		// Truy vấn trực tiếp từ Repository để tránh vòng lặp phụ thuộc (dependency loop)
		return modules.Core.ZoneRepository.GetZoneCatalog(ctx)
	})

}
