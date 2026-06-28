// ======================================================================================================
// 📂 MODULE: controlplane/internal/zone/domain/service/backpressure_service.go
//            Đặc Tả Nghiệp Vụ Quản Trị Backpressure (Zone-Scoped)
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & HỢP ĐỒNG NGHIỆP VỤ (CONTRACT & BUSINESS RULES):
//   - Định nghĩa giao diện API nghiệp vụ tiếp nhận tín hiệu backpressure từ các Zone.
//   - Mô hình Zero-Knowledge: Controlplane không lưu vết hay quản lý trạng thái từng node cụ thể,
//     chỉ giao tiếp ở cấp Zone thông qua Redis L2 Cache và Pub/Sub Fanout.
//
//     1) ZONE-SCOPED BACKPRESSURE:
//        * Nhận tín hiệu quá tải từ job-proxy qua gRPC, ghi nhận và phát tán qua CAS + Fanout.
//
//     2) ZERO INFRASTRUCTURE COUPLING:
//        * Không phụ thuộc vào chi tiết cấu hình DB hay cache cụ thể, bảo vệ tính trừu tượng nghiệp vụ.
//
// 🔄 CALLSITE FLOW:
//   - Được triển khai vật lý bởi `BackpressureService` trong gói `/core/service`.
//   - Được gọi bởi gRPC handler khi job-proxy gửi tín hiệu backpressure.
//
// ======================================================================================================

package zoneSvcInterface

import (
	"context"
)

type BackpressureService interface {
	// ReportBackpressure ghi nhận sự kiện nghẽn hàng đợi từ job-proxy và phát tán tới các replica qua Pub/Sub.
	ReportBackpressure(ctx context.Context, zoneID string, queueLen int64, pendingLen int64, congested bool, epoch int64, congestionRate float64) error
}
