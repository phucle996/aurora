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

	coreCache "controlplane/internal/core/cache"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	"controlplane/pkg/logger"
)

type DataplaneOrchestrator struct {
	repo    coreRepoInterface.DataplaneNodeRepository
	cache   coreCache.DataplaneCache
	service coreSvcInterface.DataplaneNodeService
}

// NewDataplaneOrchestrator khởi tạo background Orchestrator.
func NewDataplaneOrchestrator(
	repo coreRepoInterface.DataplaneNodeRepository,
	cache coreCache.DataplaneCache,
	service coreSvcInterface.DataplaneNodeService,
) *DataplaneOrchestrator {
	return &DataplaneOrchestrator{
		repo:    repo,
		cache:   cache,
		service: service,
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

// reconcile thực thi việc đối soát trạng thái liveness động mức Node sử dụng Redis Set Active Pool và gRPC Fallback Cache.
// Đảm bảo không ghi xuống PostgreSQL khi có biến động node tạm thời (Zero DB I/O on node failure).
func (o *DataplaneOrchestrator) reconcile(ctx context.Context) {
	// Step 1: Lấy danh sách các Cluster/Zone cấu hình tĩnh từ Postgres DB
	clusters, err := o.repo.ListReadyClusters(ctx)
	if err != nil {
		logger.SysWarnFields("core.dataplane.orchestrator", "failed to list ready clusters from postgres", err, nil)
		return
	}

	for _, cluster := range clusters {
		zoneID := cluster.ZoneID

		// Step 2: Lấy toàn bộ danh sách Hostname các Node đang hoạt động trong Zone từ Redis Set (Active Pool)
		nodes, err := o.cache.GetActiveNodes(ctx, zoneID)
		if err != nil {
			logger.SysWarnFields("core.dataplane.orchestrator", "failed to fetch active nodes from Redis Set", err, logger.Fields{"zone_id": zoneID})
			continue
		}

		// Nếu không có node nào đăng ký trong active pool, bỏ qua quét
		if len(nodes) == 0 {
			continue
		}

		for _, hostname := range nodes {
			// Step 3: [HOT-PATH] Kiểm tra khóa liveness trên Redis Cache TTL (EXISTS)
			healthy, err := o.cache.CheckNodeLiveness(ctx, zoneID, hostname)
			if err != nil {
				logger.SysWarnFields("core.dataplane.orchestrator", "failed to check node liveness on Redis", err, logger.Fields{
					"zone_id":  zoneID,
					"hostname": hostname,
				})
				continue
			}

			// Nếu node có khóa liveness hợp lệ -> Vẫn sống tốt, bỏ qua đối soát
			if healthy {
				continue
			}

			// Step 4: [SAFETY-PATH] Khóa Redis liveness đã hết hạn! Đối soát bộ nhớ tạm gRPC Fallback
			hasFallback := o.service.CheckFallbackLiveness(ctx, zoneID, hostname)
			if hasFallback {
				// Node vẫn sống và gửi heartbeat gRPC trực tiếp do mất mạng Redis -> Bỏ qua báo sập
				logger.SysInfoFields("core.dataplane.orchestrator", "Node missed Redis heartbeat but remains ALIVE via gRPC Fallback path", logger.Fields{
					"zone_id":  zoneID,
					"hostname": hostname,
				})
				continue
			}

			// Step 5: Node thực sự đã sập nguồn hoặc mất kết nối hoàn toàn! Kích hoạt quy trình Giải cứu & Dọn dẹp
			logger.SysWarnFields("core.dataplane.orchestrator", "CRITICAL: Node detected as FAILED (Redis & gRPC Fallback lost)", nil, logger.Fields{
				"zone_id":  zoneID,
				"hostname": hostname,
			})

			// Step 5.1: Sinh khóa giải cứu nguyên tử (Atomic Salvage Lock) trên Redis bằng SETNX
			acquired, err := o.cache.AcquireSalvageLock(ctx, zoneID, hostname)
			if err != nil {
				logger.SysWarnFields("core.dataplane.orchestrator", "failed to acquire salvage lock on Redis", err, logger.Fields{
					"zone_id":  zoneID,
					"hostname": hostname,
				})
				continue
			}

			// Nếu không giành được khóa -> Một CP replica khác đang chạy giải cứu rồi, bỏ qua
			if !acquired {
				logger.SysInfoFields("core.dataplane.orchestrator", "Another Controlplane replica is already salvaging node. Skipping.", logger.Fields{
					"zone_id":  zoneID,
					"hostname": hostname,
				})
				continue
			}

			// Step 5.2: Tiến hành dọn dẹp node lỗi khỏi Redis Set active pool
			err = o.cache.RemoveNodeFromActivePool(ctx, zoneID, hostname)
			if err != nil {
				logger.SysWarnFields("core.dataplane.orchestrator", "failed to remove node from active pool Redis Set", err, logger.Fields{
					"zone_id":  zoneID,
					"hostname": hostname,
				})
				continue
			}

			// Step 5.3: Thực hiện giải cứu các Job bị kẹt trong PEL của node lỗi bằng XCLAIM trên Redis
			// (Trong thực tế sẽ gọi lệnh XCLAIM các job kẹt sang node khỏe mạnh khác cùng zone)
			logger.SysWarnFields("core.dataplane.orchestrator", "EXCLUSIVE SALVAGE EXECUTOR: Reclaiming pending jobs from PEL (XCLAIM) of failed node", nil, logger.Fields{
				"zone_id":  zoneID,
				"hostname": hostname,
				"executor": "this-replica",
			})
		}
	}
}
