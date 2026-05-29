// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/domain/entity/dataplane_node.go
//            Đặc Tả Thực Thể Nghiệp Vụ (Domain Entity) Của Cụm Dataplane Cluster
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & QUY TRÌNH LIFECYCLE (CONTRACT & LIFECYCLE STATES):
//   - Định nghĩa mô hình thực thể domain strongly-typed đại diện cho cụm Dataplane Cluster ở tầng Core.
//   - Định vị vòng đời hoạt động của cụm thông qua tập hợp trạng thái (DataplaneNodeStatus) nghiêm ngặt:
//
//     * DataplaneNodeStatusRegistered: Đã được đăng ký định danh hạ tầng nhưng chưa được kiểm chứng.
//     * DataplaneNodeStatusReady: Cụm hoạt động hoàn hảo, có heartbeat/lease sống động, sẵn sàng định tuyến.
//     * DataplaneNodeStatusDegraded: Hoạt động suy giảm hiệu năng (có lỗi nhẹ nhưng vẫn xử lý được).
//     * DataplaneNodeStatusDraining: Đang trong tiến trình rút tải (drain), không phân bổ thêm công việc mới.
//     * DataplaneNodeStatusStale: Hết hạn lease tạm thời (quá 30s), cảnh báo nguy cơ sập.
//     * DataplaneNodeStatusFailed: Xác nhận sập thực tế (quá 90s hoặc Fast-Path kích hoạt), trigger failover.
//     * DataplaneNodeStatusMaintenance: Đang bảo trì định kỳ bởi SRE.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Database PostgreSQL (bảng `core.dataplane_nodes`) lưu trữ snapshot cấu hình mong muốn (Desired Configuration)
//     và trạng thái lâu dài bền vững của cụm.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - **Tách biệt tuyệt đối**: File này chỉ chứa cấu trúc dữ liệu domain thuần túy, tuyệt đối không chứa
//     bất kỳ logic persistence (SQL/Redis driver) hay logic truyền dữ liệu qua mạng nào.
//   - **Không chứa logic validation động**: Đóng vai trò là data-carrier an toàn, strongly-typed.
//
// 🔄 CALLSITE FLOW:
//   - Được sử dụng trực tiếp bởi `DataplaneNodeService` để áp dụng các business rules và chuyển đổi trạng thái.
//   - Được mapper chuyển đổi qua lại với `coreModel.DataplaneNode` DTO phục vụ DB & API Layer.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - Trạng thái `Ready` là điều kiện tiên quyết để các module nghiệp vụ khác (như Mail Module)
//     lựa chọn cụm để routing công việc. Mọi sự thay đổi sang `Failed` sẽ kích hoạt báo động cấp 1.
//
// ======================================================================================================

package coreEntity

import "time"

type DataplaneNodeStatus string

const (
	DataplaneNodeStatusRegistered  DataplaneNodeStatus = "registered"
	DataplaneNodeStatusReady       DataplaneNodeStatus = "ready"
	DataplaneNodeStatusDegraded    DataplaneNodeStatus = "degraded"
	DataplaneNodeStatusDraining    DataplaneNodeStatus = "draining"
	DataplaneNodeStatusStale       DataplaneNodeStatus = "stale"
	DataplaneNodeStatusFailed      DataplaneNodeStatus = "failed"
	DataplaneNodeStatusMaintenance DataplaneNodeStatus = "maintenance"
)

type DataplaneNode struct {
	ID        string              `json:"id"`        // UUIDv7 định danh duy nhất của cụm
	Status    DataplaneNodeStatus `json:"status"`    // Trạng thái lifecycle hiện tại của cụm
	ZoneID    string              `json:"zone_id"`    // Khóa ngoại liên kết duy nhất 1-1 tới Zone
	Endpoint  string              `json:"endpoint"`  // Địa chỉ gRPC/HTTP Load Balancer URL của cụm
	CreatedAt time.Time           `json:"created_at"`
	UpdatedAt time.Time           `json:"updated_at"`
}
