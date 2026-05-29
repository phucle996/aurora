// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/domain/repo/dataplane_node_repo.go
//            Đặc Tả Giao Diện Lưu Trữ (Repository Interface) Của Dataplane Registry
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & TRỪU TƯỢNG HÓA HẠ TẦNG (CONTRACT & STORAGE ABSTRACT LAYER):
//   - Định nghĩa hợp đồng giao tiếp dữ liệu bền vững (Durable State) cho cụm Dataplane Cluster.
//   - Thiết lập chuẩn giao diện sạch sẽ, trừu tượng hóa toàn bộ cơ sở dữ liệu vật lý PostgreSQL:
//
//     1) ĐỘC LẬP TỔNG THỂ (CLEAN ARCHITECTURE BOUNDARY):
//        * Tầng nghiệp vụ (Service Layer) chỉ giao tiếp qua giao diện này, tuyệt đối không được phép
//          biết các chi tiết cài đặt vật lý, thư viện driver hay câu lệnh SQL thô.
//
//     2) HOÀN TOÀN MOCKABLE (TESTABILITY):
//        * Hỗ trợ tuyệt đối cho việc viết Unit Test bằng cách tạo dựng các mock repository dễ dàng,
//          không phụ thuộc vào việc kết nối DB thật.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - PostgreSQL DB là nguồn tin cậy duy nhất của desired state.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Repository Interface đóng vai trò là ranh giới cô lập giữa Domain Core Logic và Infrastructure Layer.
//   - Không cho phép rò rỉ bất kỳ chi tiết SQL/driver nào vượt qua ranh giới này.
//
// 🔄 CALLSITE FLOW:
//   - Được triển khai vật lý bởi `DataplaneNodeRepoImpl` trong gói `/core/repository`.
//   - Được inject trực tiếp vào `DataplaneNodeService` và `DataplaneOrchestrator` khi khởi động hệ thống.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Cần đảm bảo các connection pool kết nối PostgreSQL được quản lý vòng đời đúng chuẩn,
//     giải phóng tài nguyên an toàn khi tắt cụm.
//
// ======================================================================================================

package coreRepoInterface

import (
	"context"
	coreEntity "controlplane/internal/core/domain/entity"

	"github.com/google/uuid"
)

type DataplaneNodeRepository interface {
	// RegisterCluster thực hiện đăng ký hoặc cập nhật cấu hình/endpoint của cụm Dataplane theo phương thức idempotent.
	RegisterCluster(ctx context.Context, cluster coreEntity.DataplaneNode) error

	// UpdateClusterStatus cập nhật trực tiếp trạng thái lifecycle của cụm Dataplane.
	UpdateClusterStatus(ctx context.Context, id uuid.UUID, status coreEntity.DataplaneNodeStatus) error

	// GetCluster lấy chi tiết thông tin của một cụm Dataplane theo ID duy nhất.
	GetCluster(ctx context.Context, id uuid.UUID) (*coreEntity.DataplaneNode, error)

	// GetClusterByZone tìm kiếm thông tin cụm Dataplane được gán cho một Zone cụ thể.
	GetClusterByZone(ctx context.Context, zoneID uuid.UUID) (*coreEntity.DataplaneNode, error)

	// ListReadyClusters liệt kê toàn bộ các cụm Dataplane đang ở trạng thái hoạt động 'ready'.
	ListReadyClusters(ctx context.Context) ([]coreEntity.DataplaneNode, error)
}
