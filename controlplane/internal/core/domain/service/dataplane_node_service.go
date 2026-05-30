// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/domain/service/dataplane_node_service.go
//            Đặc Tả Nghiệp Vụ Điều Phối (Service Interface) Dataplane Node
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & HỢP ĐỒNG NGHIỆP VỤ (CONTRACT & BUSINESS RULES):
//   - Định nghĩa giao diện API nghiệp vụ cốt lõi (Business Public API) quản trị Dataplane Cluster.
//   - Đóng vai trò là trung tâm điều phối trạng thái, routing shard và failover của toàn bộ mặt phẳng.
//
//     1) FAST FAIL-FAST PROBING DECISION ENGINE:
//        * Cung cấp các phương thức để nhanh chóng đối soát trạng thái liveness của Dataplane dưới 2s,
//          tự động điều phối các chiến lược dự phòng failover.
//
//     2) ZERO INFRASTRUCTURE COUPLING:
//        * Không bị phụ thuộc vào các chi tiết cấu hình DB hay cache cụ thể, bảo vệ tính trừu tượng nghiệp vụ.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Tổng hợp dữ liệu từ PostgreSQL (Durable State) và Redis (Transient Lease State) để đưa ra trạng thái
//     thực tế chính xác và an toàn nhất.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Cổng giao tiếp chính độc quyền của toàn bộ hệ thống khi muốn tương tác với Dataplane.
//   - Cách ly hoàn toàn các module bên ngoài khỏi sự phức tạp của tầng quản trị hạ tầng và caching.
//
// 🔄 CALLSITE FLOW:
//   - Được triển khai vật lý bởi `DataplaneNodeService` trong gói `/core/service`.
//   - Được gọi trực tiếp bởi các external module (như Mail Module) để xác định tính hợp lệ của zone.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Mọi thao tác truy xuất đều được tối ưu hóa để đảm bảo latency ở mức P99 < 5ms trong điều kiện chịu tải cao.
//
// ======================================================================================================

package coreSvcInterface

import (
	"context"
	coreEntity "controlplane/internal/core/domain/entity"
)

type DataplaneNodeService interface {
	// IngestHeartbeat ghi nhận nhịp tim thời gian thực từ cụm Dataplane gửi lên, gia hạn lease trên Redis và cập nhật DB Postgres.
	IngestHeartbeat(ctx context.Context, clusterID string, zoneID string) error

	// VerifyClusterStatus thực hiện Fast Fail-Fast Probing: Kiểm tra nhanh Redis Lease. Nếu mất lease, cập nhật Postgres thành 'failed' lập tức.
	VerifyClusterStatus(ctx context.Context, zoneID string) (string, error)

	// GetEligibleClusterForZone tìm kiếm cụm Dataplane khỏe mạnh (ready) và sẵn sàng chạy dịch vụ tương ứng tại một Zone active.
	GetEligibleClusterForZone(ctx context.Context, zoneID string, serviceType string) (*coreEntity.DataplaneNode, error)

	// IngestFallbackHeartbeat ghi nhận nhịp tim dự phòng qua kênh gRPC trực tiếp khi Redis sập.
	// Nhịp tim này được lưu vết vào bộ nhớ tạm để tránh báo tử nhầm node.
	IngestFallbackHeartbeat(ctx context.Context, hostname string, zoneID string) error

	// CheckFallbackLiveness kiểm tra xem node có nhịp tim dự phòng hợp lệ (trong vòng 8s qua) trong bộ nhớ tạm hay không.
	CheckFallbackLiveness(ctx context.Context, zoneID string, hostname string) bool
}
