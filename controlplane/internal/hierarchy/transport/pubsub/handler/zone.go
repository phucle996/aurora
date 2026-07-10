// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/transport/pubsub/handler/zone.go
//            NATS Handler cho ZoneService - phục vụ đồng bộ Zone và phân giải Zone cho Edge/ACR qua NATS
// ======================================================================================================

package pubsubHandler

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	coreSvcInterface "controlplane/internal/hierarchy/domain/service"
	coreProto "controlplane/internal/hierarchy/transport/rpc/proto"
	"controlplane/internal/observability"
	"controlplane/pkg/logger"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// ZoneNatsHandler tiếp nhận các yêu cầu đồng bộ Zone qua NATS.
type ZoneNatsHandler struct {
	cfg         *config.Config
	zoneService coreSvcInterface.ZoneService
	otel        *observability.OTel
}

// NewZoneNatsHandler khởi tạo NATS Handler cho ZoneService.
func NewZoneNatsHandler(
	cfg *config.Config,
	zoneService coreSvcInterface.ZoneService,
	otel *observability.OTel,
) *ZoneNatsHandler {
	return &ZoneNatsHandler{
		cfg:         cfg,
		zoneService: zoneService,
		otel:        otel,
	}
}

// Subscribe đăng ký lắng nghe các sự kiện đồng bộ và phân giải Zone qua NATS Core.
func (h *ZoneNatsHandler) Subscribe(nc *nats.Conn) ([]*nats.Subscription, error) {
	const queueGroup = "hierarchy_zone_service"
	var subs []*nats.Subscription

	// 1. LUỒNG ĐỒNG BỘ DANH SÁCH ZONES (GetZoneList)
	subGetList, err := nc.QueueSubscribe("core.zone.get_zone_list", queueGroup, func(msg *nats.Msg) {
		ctx := context.Background()
		if msg.Header != nil {
			traceparent := msg.Header.Get("traceparent")
			if traceparent != "" {
				ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
			}
		}

		var span trace.Span
		if h.otel != nil {
			ctx, span = h.otel.StartServerSpan(ctx, "NATS core.zone.get_zone_list")
			defer span.End()
			span.SetAttributes(
				attribute.String("messaging.system", "nats"),
				attribute.String("messaging.destination", "core.zone.get_zone_list"),
			)
		}

		respondError := func(errMsg string) {
			logger.SysError("NATS.GetZoneList", errMsg)
			_ = msg.Respond([]byte{})
		}

		var req coreProto.GetZoneListRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			respondError("failed to unmarshal request payload")
			return
		}

		zones, err := h.zoneService.AcrListZones(ctx)
		if err != nil {
			respondError(fmt.Sprintf("failed to list zones: %v", err))
			return
		}

		var pbZones []*coreProto.ZoneEntry
		for _, z := range zones {
			pbZones = append(pbZones, &coreProto.ZoneEntry{
				ZoneId:   z.ID.String(),
				ZoneCode: z.Code,
				Status:   string(z.Status),
				Name:     z.Name,
			})
		}

		resp := &coreProto.GetZoneListResponse{Zones: pbZones}
		respData, err := proto.Marshal(resp)
		if err != nil {
			respondError("failed to marshal response")
			return
		}

		_ = msg.Respond(respData)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to core.zone.get_zone_list: %w", err)
	}
	subs = append(subs, subGetList)

	// 2. LUỒNG PHÂN GIẢI CỤ THỂ 1 ZONE (ResolveZone)
	subResolve, err := nc.QueueSubscribe("core.zone.resolve_zone", queueGroup, func(msg *nats.Msg) {
		ctx := context.Background()
		if msg.Header != nil {
			traceparent := msg.Header.Get("traceparent")
			if traceparent != "" {
				ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
			}
		}

		var span trace.Span
		if h.otel != nil {
			ctx, span = h.otel.StartServerSpan(ctx, "NATS core.zone.resolve_zone")
			defer span.End()
			span.SetAttributes(
				attribute.String("messaging.system", "nats"),
				attribute.String("messaging.destination", "core.zone.resolve_zone"),
			)
		}

		respondError := func(errMsg string) {
			logger.SysError("NATS.ResolveZone", errMsg)
			resp := &coreProto.ResolveZoneResponse{Found: false}
			if respData, err := proto.Marshal(resp); err == nil {
				_ = msg.Respond(respData)
			}
		}

		var req coreProto.ResolveZoneRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			respondError("failed to unmarshal ResolveZoneRequest")
			return
		}

		zones, err := h.zoneService.AcrListZones(ctx)
		if err != nil {
			respondError(fmt.Sprintf("failed to list zones for resolution: %v", err))
			return
		}

		for _, z := range zones {
			if z.Code == req.ZoneCode {
				resp := &coreProto.ResolveZoneResponse{
					Found:  true,
					ZoneId: z.ID.String(),
					Status: string(z.Status),
					Name:   z.Name,
				}
				respData, err := proto.Marshal(resp)
				if err != nil {
					respondError("failed to marshal ResolveZoneResponse")
					return
				}
				_ = msg.Respond(respData)
				return
			}
		}

		resp := &coreProto.ResolveZoneResponse{Found: false}
		respData, _ := proto.Marshal(resp)
		_ = msg.Respond(respData)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to core.zone.resolve_zone: %w", err)
	}
	subs = append(subs, subResolve)

	return subs, nil
}
