// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/service/dataplane_orchestrator.go
//            Hiện Thực Hóa Logic Giám Sát Background Dataplane Orchestrator
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & SỰ PHÙ HỢP HOẠT ĐỘNG (CONTRACT & SYSTEM SRE ALIGNMENT):
//   - Triển khai background worker giám sát sự sống động thứ cấp (Safety Fallback Path) cho toàn bộ
//     hạ tầng Dataplane Cluster.
//   - Đảm bảo tính sẵn sàng cao (High-Availability - HA) và hoạt động bền bỉ, an toàn trong cloud native:
//
//     1) GRACEFUL LIFECYCLE SHUTDOWN (HA CONTAINER COMPATIBILITY):
//        * Lắng nghe tín hiệu kết thúc từ Context cha để hủy vòng lặp và kết thúc goroutine background
//          ngay lập tức khi container scale down/restart, triệt tiêu hoàn toàn rò rỉ (leak) bộ nhớ.
//
//     2) 30S & 90S HEARTBEAT WINDOWS:
//        * Đóng vai trò là chốt chặn an toàn thứ cấp khi không có request nghiệp vụ kích hoạt Fast Path.
//        * Tự động degrade status sang `stale` sau 30 giây không có lease, và chuyển sang `failed`
//          sau 90 giây (Double Heartbeat Window) để chuẩn bị các tiến trình failover.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Thao tác trực tiếp với Postgres DB (ListReadyClusters, UpdateClusterStatus) và Redis (CheckLeaseExists)
//     để đồng bộ Desired State lâu dài của hệ thống.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Chạy nền độc lập trong một goroutine biệt lập của Core Module, tự phục hồi khi có lỗi phụ thuộc.
//   - Không chặn đứng tiến trình chính hoặc ném ra exception làm sập app (Zero Panic Promise).
//
// 🔄 CALLSITE FLOW:
//   - Được quản lý vòng đời bởi DI Module (`core/module.go`). Tự động khởi chạy luồng Start
//     khi module Core Bootstrap.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Định kỳ quét 10s là khoảng cách thời gian cân bằng lý tưởng giữa tài nguyên CPU và độ nhạy phát hiện lỗi.
//
// ======================================================================================================

package coreSvcImpl

import (
	"context"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreCache "controlplane/internal/core/cache"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
)

type DataplaneOrchestrator struct {
	repo  coreRepoInterface.DataplaneNodeRepository
	cache coreCache.DataplaneCache
}

// NewDataplaneOrchestrator khởi tạo background Orchestrator.
func NewDataplaneOrchestrator(
	repo coreRepoInterface.DataplaneNodeRepository,
	cache coreCache.DataplaneCache,
) *DataplaneOrchestrator {
	return &DataplaneOrchestrator{
		repo:  repo,
		cache: cache,
	}
}

// Start khởi chạy vòng lặp vô hạn giám sát liveness dưới background goroutine, hỗ trợ graceful shutdown.
func (o *DataplaneOrchestrator) Start(ctx context.Context) {
	// Step 1: Thiết lập Ticker chạy định kỳ mỗi 10 giây.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	logger.SysInfoFields("core.dataplane.orchestrator", "started background dataplane status loop", nil)

	// Step 2: Vòng lặp select giám sát tín hiệu kết thúc hoặc tick thời gian.
	for {
		select {
		case <-ctx.Done():
			// Tín hiệu graceful shutdown được phát ra -> thoát vòng lặp an toàn.
			logger.SysInfoFields("core.dataplane.orchestrator", "stopping background dataplane status loop", nil)
			return
		case <-ticker.C:
			// Mỗi nhịp tick, tiến hành chạy đối khớp trạng thái.
			o.reconcile(ctx)
		}
	}
}

// reconcile thực thi việc quét các cụm đang hoạt động trong Postgres và so sánh với Redis Lease để cập nhật trạng thái tương ứng.
func (o *DataplaneOrchestrator) reconcile(ctx context.Context) {
	// Step 1: Quét toàn bộ danh sách các cụm đang có status 'ready' trong Postgres.
	clusters, err := o.repo.ListReadyClusters(ctx)
	if err != nil {
		logger.SysWarnFields("core.dataplane.orchestrator", "failed to list ready clusters from postgres", err, nil)
		return
	}

	now := time.Now().UTC()
	for _, cluster := range clusters {
		// Step 2: Kiểm tra sự tồn tại của Lease Key trên Redis của cụm đó.
		exists, err := o.cache.CheckLeaseExists(ctx, cluster.ZoneID)
		if err != nil {
			logger.SysWarnFields("core.dataplane.orchestrator", "failed to check lease on redis", err, logger.Fields{"zone": cluster.ZoneID})
			continue
		}

		if exists {
			// Cụm vẫn healthy và có heartbeat đều đặn -> bỏ qua.
			continue
		}

		// Step 3: [Lease Expired] Tính toán khoảng thời gian từ lần cập nhật cuối cùng trong DB đến hiện tại.
		durationSinceUpdate := now.Sub(cluster.UpdatedAt)
		parsedClusterID, _ := uuid.Parse(cluster.ID)

		// Step 4: Chốt chặn 90 giây -> Chuyển sang FAILED.
		if durationSinceUpdate >= 90*time.Second {
			err = o.repo.UpdateClusterStatus(ctx, parsedClusterID, coreEntity.DataplaneNodeStatusFailed)
			if err != nil {
				logger.SysWarnFields("core.dataplane.orchestrator", "failed to transition status to failed", err, logger.Fields{"cluster": cluster.ID})
			} else {
				logger.SysWarnFields("core.dataplane.orchestrator", "cluster transitioned to FAILED due to lease expiration over 90s", nil, logger.Fields{"cluster": cluster.ID, "zone": cluster.ZoneID})
			}
		// Step 5: Chốt chặn 30 giây -> Chuyển sang STALE.
		} else if durationSinceUpdate >= 30*time.Second && cluster.Status == coreEntity.DataplaneNodeStatusReady {
			err = o.repo.UpdateClusterStatus(ctx, parsedClusterID, coreEntity.DataplaneNodeStatusStale)
			if err != nil {
				logger.SysWarnFields("core.dataplane.orchestrator", "failed to transition status to stale", err, logger.Fields{"cluster": cluster.ID})
			} else {
				logger.SysWarnFields("core.dataplane.orchestrator", "cluster transitioned to STALE due to lease expiration over 30s", nil, logger.Fields{"cluster": cluster.ID, "zone": cluster.ZoneID})
			}
		}
	}
}
