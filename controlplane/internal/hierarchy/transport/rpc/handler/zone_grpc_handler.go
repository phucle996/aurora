// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/transport/rpc/handler/zone_grpc_handler.go
//            gRPC Handler cho ZoneService - phục vụ đồng bộ Zone và phân giải Zone cho Edge/ACR
// ======================================================================================================

package coreRpcHandler

import (
	"context"

	coreSvcInterface "controlplane/internal/hierarchy/domain/service"
	coreProto "controlplane/internal/hierarchy/transport/rpc/proto"
	"controlplane/pkg/logger"
)

// ZoneGRPCHandler tiếp nhận các yêu cầu đồng bộ Zone qua gRPC.
type ZoneGRPCHandler struct {
	coreProto.UnimplementedZoneServiceServer
	service coreSvcInterface.ZoneService
}

// NewZoneGRPCHandler khởi tạo gRPC Handler cho ZoneService.
func NewZoneGRPCHandler(service coreSvcInterface.ZoneService) *ZoneGRPCHandler {
	return &ZoneGRPCHandler{service: service}
}

// GetZoneList trả về danh sách Zone để ACR khởi tạo L1 cache khi boot.
func (h *ZoneGRPCHandler) GetZoneList(ctx context.Context, req *coreProto.GetZoneListRequest) (*coreProto.GetZoneListResponse, error) {
	const op = "core.zone.rpc.get_zone_list"

	zones, err := h.service.RPCListZones(ctx)
	if err != nil {
		logger.RPCHandlerWarn(ctx, op, err, "failed to list zones via gRPC")
		return nil, err
	}

	var pbZones []*coreProto.ZoneEntry
	for _, z := range zones {
		// [COMMENT]: Map các trường ID, Code, Status, Name vào ZoneEntry protobuf
		pbZones = append(pbZones, &coreProto.ZoneEntry{
			ZoneId:   z.ID.String(),
			ZoneCode: z.Code,
			Status:   string(z.Status),
			Name:     z.Name,
		})
	}

	return &coreProto.GetZoneListResponse{Zones: pbZones}, nil
}

// ResolveZone phân giải zone theo code, phục vụ lazy-load từ ACR khi miss L1/L2.
func (h *ZoneGRPCHandler) ResolveZone(ctx context.Context, req *coreProto.ResolveZoneRequest) (*coreProto.ResolveZoneResponse, error) {
	const op = "core.zone.rpc.resolve_zone"

	zones, err := h.service.RPCListZones(ctx)
	if err != nil {
		logger.RPCHandlerWarn(ctx, op, err, "failed to list zones for resolution")
		return nil, err
	}

	for _, z := range zones {
		if z.Code == req.ZoneCode {
			return &coreProto.ResolveZoneResponse{
				Found:  true,
				ZoneId: z.ID.String(),
				Status: string(z.Status),
				Name:   z.Name,
			}, nil
		}
	}

	// [COMMENT]: Not found trả về found=false để ACR ghi negative cache
	return &coreProto.ResolveZoneResponse{Found: false}, nil
}
