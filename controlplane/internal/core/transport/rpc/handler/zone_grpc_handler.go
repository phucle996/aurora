// ======================================================================================================
// 📂 MODULE: controlplane/internal/core/transport/rpc/handler/zone_grpc_handler.go
//            Hiện Thực Hóa gRPC Server Handler Cho ZoneService Phục Vụ ACL Service
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & SỰ PHÙ HỢP AN TOÀN (DESIGN CONTRACT & ZERO TRUST):
//   - Cung cấp cổng tiếp nhận thông tin Zone (ID và Code) phục vụ đồng bộ L1 cache cho ACL.
//   - Logic xử lý tối giản, truy xuất qua L1 RAM cache của Core Service.
//
// 🔒 RANH GIỚI BẢO MẬT & KIẾN TRÚC (CRITICAL ARCHITECTURAL BOUNDARY):
//   - Đóng vai trò là Transport Layer, chịu trách nhiệm map lỗi gRPC thô và unwrap errors.
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

// ZoneGRPCHandler tiếp nhận các yêu cầu đồng bộ danh sách Zone qua gRPC.
type ZoneGRPCHandler struct {
	coreProto.UnimplementedZoneServiceServer
	service coreSvcInterface.ZoneService
}

// NewZoneGRPCHandler khởi tạo gRPC Handler cho ZoneService.
func NewZoneGRPCHandler(service coreSvcInterface.ZoneService) *ZoneGRPCHandler {
	// [COMMENT]: Gán dependency ZoneService
	return &ZoneGRPCHandler{
		service: service,
	}
}

// GetZoneList trả về danh sách các Zone bao gồm ID và Code phục vụ L1 cache của ACL.
func (h *ZoneGRPCHandler) GetZoneList(ctx context.Context, req *coreProto.GetZoneListRequest) (*coreProto.GetZoneListResponse, error) {
	// [COMMENT]: Gọi xuống Service layer để lấy danh sách zone từ cache RAM L1
	zones, err := h.service.ListZones(ctx)
	if err != nil {
		logger.SysWarnFields("core.zone.rpc", "failed to list zones via gRPC", err, nil)
		return nil, status.Errorf(codes.Internal, "failed to list zones: %v", err)
	}

	var pbZones []*coreProto.ZoneEntry
	for _, z := range zones {
		pbZones = append(pbZones, &coreProto.ZoneEntry{
			ZoneId:   z.ID.String(),
			ZoneCode: z.Code,
		})
	}

	return &coreProto.GetZoneListResponse{Zones: pbZones}, nil
}
