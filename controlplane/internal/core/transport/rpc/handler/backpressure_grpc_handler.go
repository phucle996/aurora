// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/transport/rpc/handler/backpressure_grpc_handler.go
//            Hiện Thực Hóa gRPC Server Handler Tiếp Nhận Backpressure Từ Job-Proxy
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & SỰ PHÙ HỢP AN TOÀN (DESIGN CONTRACT & ZERO TRUST):
//   - Cung cấp cổng tiếp nhận tín hiệu quá tải (Backpressure) từ job-proxy qua gRPC.
//   - Bảo đảm tính an toàn bảo mật Zero Trust trong môi trường cloud native:
//
//     1) mTLS FORCED SECURITY BOUNDARY (XÁC THỰC CỨNG 2 CHIỀU):
//        * Handler này chỉ được kích hoạt và lắng nghe trên gRPC Server đã cấu hình mTLS bắt buộc
//          (`ClientAuth: tls.RequireAndVerifyClientCert`).
//
//     2) PEAK LATENCY OPTIMIZATION:
//        * Logic xử lý được thiết kế tối giản, nhanh chóng gọi xuống Core Service Layer.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Đóng vai trò là Transport Layer, chịu trách nhiệm map lỗi gRPC thô (gRPC status codes) và unwrap errors.
//   - Không chứa bất kỳ logic nghiệp vụ kiểm tra trạng thái hay thay đổi trực tiếp Database/Redis.
//
// ======================================================================================================

package coreRpcHandler

import (
	"context"

	coreSvcInterface "controlplane/internal/core/domain/service"
	coreProto "controlplane/internal/core/transport/rpc/proto"
	"controlplane/pkg/logger"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// BackpressureGRPCHandler tiếp nhận tín hiệu quá tải từ job-proxy qua gRPC.
type BackpressureGRPCHandler struct {
	coreProto.UnimplementedBackpressureServiceServer
	service coreSvcInterface.BackpressureService
}

// NewBackpressureGRPCHandler khởi tạo gRPC Handler cho Backpressure.
func NewBackpressureGRPCHandler(service coreSvcInterface.BackpressureService) *BackpressureGRPCHandler {
	return &BackpressureGRPCHandler{
		service: service,
	}
}

// ReportBackpressure tiếp nhận cuộc gọi gRPC báo cáo tải từ job-proxy và chuyển tiếp xuống service để cập nhật L2 + Fanout.
func (h *BackpressureGRPCHandler) ReportBackpressure(ctx context.Context, req *coreProto.ReportBackpressureRequest) (*coreProto.ReportBackpressureResponse, error) {
	// Step 1: Kiểm tra tính hợp lệ của tham số đầu vào.
	if req.GetZoneId() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "zone_id is required")
	}

	// Step 2: Gọi xuống Service layer để xử lý logic lưu trữ L2 và phát tán trạng thái.
	err := h.service.ReportBackpressure(ctx, req.GetZoneId(), req.GetQueueLen(), req.GetPendingLen(), req.GetCongested(), req.GetEpoch(), req.GetCongestionRate())
	if err != nil {
		logger.SysWarnFields("core.backpressure.rpc", "failed to process backpressure report via gRPC", err, logger.Fields{"zone": req.GetZoneId()})
		return nil, status.Errorf(codes.Internal, "failed to process backpressure report: %v", err)
	}

	return &coreProto.ReportBackpressureResponse{Success: true}, nil
}
