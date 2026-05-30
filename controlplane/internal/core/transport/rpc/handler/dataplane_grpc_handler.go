// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/transport/rpc/handler/dataplane_grpc_handler.go
//            Hiện Thực Hóa gRPC Server Handler Tiếp Nhận Fallback Heartbeat (Fallback Path)
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & SỰ PHÙ HỢP AN TOÀN (DESIGN CONTRACT & ZERO TRUST):
//   - Cung cấp cổng tiếp nhận nhịp tim dự phòng (Fallback Path) thông qua giao thức gRPC Server.
//   - Bảo đảm tính an toàn bảo mật Zero Trust tuyệt đối trong môi trường cloud native:
//
//     1) mTLS FORCED SECURITY BOUNDARY (XÁC THỰC CỨNG 2 CHIỀU):
//        * Handler này chỉ được kích hoạt và lắng nghe trên gRPC Server đã cấu hình mTLS bắt buộc
//          (`ClientAuth: tls.RequireAndVerifyClientCert`).
//        * Chặn đứng hoàn toàn các truy cập giả mạo, chỉ cho phép các cụm Dataplane có Client Certificate
//          hợp lệ được ký bởi Agent CA của Controlplane kết nối và thực hiện cuộc gọi.
//
//     2) PEAK LATENCY OPTIMIZATION:
//        * Logic xử lý được thiết kế tối giản, nhanh chóng gọi xuống Core Service Layer để gia hạn Redis Lease
//          và cập nhật PostgreSQL.
//
//     3) PROMETHEUS TELEMETRY INTEGRATION:
//        * Đếm chính xác số lượng nhịp tim Fallback qua Prometheus Counter (`ObserveHeartbeat`)
//          với label path="grpc" để phục vụ việc giám sát chất lượng và khả năng tự phục hồi của hệ thống.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - PostgreSQL DB và Redis Lease thông qua `DataplaneNodeService` làm nguồn tin cậy của trạng thái.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Đóng vai trò là Transport Layer, chịu trách nhiệm map lỗi gRPC thô (gRPC status codes) và unwrap errors.
//   - Không chứa bất kỳ logic nghiệp vụ kiểm tra trạng thái hay thay đổi trực tiếp Database/Redis.
//
// 🔄 CALLSITE FLOW:
//   - Được đăng ký vào gRPC Server dùng chung của ứng dụng trong quá trình Bootstrap.
//
// 🚀 LƯU Ý VẬN HÀNH TRÊN PRODUCTION:
//   - SRE cần theo dõi log gRPC Server. Nếu có nhiều log `accepted runtime client certificate` đi kèm
//     lỗi gRPC, cần kiểm tra tính toàn vẹn của database hoặc quyền kết nối.
//
// ======================================================================================================

package coreRpcHandler

import (
	"context"

	coreSvcInterface "controlplane/internal/core/domain/service"
	coreMetric "controlplane/internal/core/metrics"
	coreProto "controlplane/internal/core/transport/rpc/proto"
	"controlplane/pkg/logger"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DataplaneGRPCHandler struct {
	coreProto.UnimplementedDataplaneRegistryServiceServer
	service coreSvcInterface.DataplaneNodeService
}

// NewDataplaneGRPCHandler khởi tạo gRPC Handler cho Dataplane Registry.
func NewDataplaneGRPCHandler(service coreSvcInterface.DataplaneNodeService) *DataplaneGRPCHandler {
	return &DataplaneGRPCHandler{
		service: service,
	}
}

// Heartbeat thực thi API gRPC tiếp nhận nhịp tim từ các cụm Dataplane (Fallback Path).
func (h *DataplaneGRPCHandler) Heartbeat(ctx context.Context, req *coreProto.HeartbeatRequest) (*coreProto.HeartbeatResponse, error) {
	// Step 1: Kiểm tra tính hợp lệ của tham số đầu vào.
	if req.GetClusterId() == "" || req.GetZoneId() == "" {
		coreMetric.ObserveHeartbeat("grpc", "failure")
		return nil, status.Errorf(codes.InvalidArgument, "cluster_id and zone_id are required")
	}

	// Step 2: Gọi xuống Core Service Layer để ghi nhận nhịp tim dự phòng vào bộ nhớ tạm cực nhanh (Zero DB I/O).
	err := h.service.IngestFallbackHeartbeat(ctx, req.GetClusterId(), req.GetZoneId())
	if err != nil {
		// Ghi nhận telemetry metrics thất bại
		coreMetric.ObserveHeartbeat("grpc", "failure")
		logger.SysWarnFields("core.dataplane.rpc", "failed to ingest fallback heartbeat via gRPC", err, logger.Fields{"cluster": req.GetClusterId(), "zone": req.GetZoneId()})
		return nil, status.Errorf(codes.Internal, "failed to ingest fallback heartbeat: %v", err)
	}

	// Step 3: Ghi nhận telemetry metrics thành công
	coreMetric.ObserveHeartbeat("grpc", "success")

	// Step 4: Trả về kết quả thành công cho Dataplane.
	return &coreProto.HeartbeatResponse{Success: true}, nil
}
