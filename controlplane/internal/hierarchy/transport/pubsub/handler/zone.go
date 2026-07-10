// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/transport/pubsub/handler/zone.go
//            NATS Handler cho ZoneService - phục vụ đồng bộ Zone và phân giải Zone cho Edge/ACR qua NATS
// ======================================================================================================

package pubsubHandler

import (
	"context"
	"fmt"
	"time"

	"controlplane/internal/config"
	coreSvcInterface "controlplane/internal/hierarchy/domain/service"
	coreProto "controlplane/internal/hierarchy/transport/proto"
	"controlplane/internal/observability"
	"controlplane/pkg/context"
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

// HandleGetZoneList xử lý yêu cầu lấy danh sách Zone, unmarshal payload thô và chuyển giao xuống service layer.
func (h *ZoneNatsHandler) HandleGetZoneList(msg *nats.Msg) {
	const op = "core.zone.rpc.get_zone_list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), op), 5*time.Second)
	defer cancel()

	if msg.Header != nil && h.otel != nil {
		traceparent := msg.Header.Get("traceparent")
		if traceparent != "" {
			ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
		}
	}

	var span trace.Span
	if h.otel != nil {
		ctx, span = h.otel.StartServerSpan(ctx, "NATS " + op)
		defer span.End()
		span.SetAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination", op),
		)
	}

	respondError := func(errMsg string) {
		logger.HandlerErrorCtx(ctx, op, fmt.Errorf("%s", errMsg))
		_ = msg.Respond([]byte{})
	}

	var req coreProto.GetZoneListRequest
	if err := proto.Unmarshal(msg.Data, &req); err != nil {
		respondError("failed to unmarshal request payload")
		return
	}

	// Gọi xuống Service Layer xử lý thuần business
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
}

// HandleResolveZone xử lý yêu cầu phân giải một Zone cụ thể, unmarshal payload thô và chuyển giao xuống service layer.
func (h *ZoneNatsHandler) HandleResolveZone(msg *nats.Msg) {
	const op = "core.zone.rpc.resolve_zone"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), op), 5*time.Second)
	defer cancel()

	if msg.Header != nil && h.otel != nil {
		traceparent := msg.Header.Get("traceparent")
		if traceparent != "" {
			ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
		}
	}

	var span trace.Span
	if h.otel != nil {
		ctx, span = h.otel.StartServerSpan(ctx, "NATS " + op)
		defer span.End()
		span.SetAttributes(
			attribute.String("messaging.system", "nats"),
			attribute.String("messaging.destination", op),
		)
	}

	respondError := func(errMsg string) {
		logger.HandlerErrorCtx(ctx, op, fmt.Errorf("%s", errMsg))
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

	// Gọi xuống Service Layer xử lý thuần business để phân giải Zone
	zone, err := h.zoneService.AcrResolveZone(ctx, req.ZoneCode)
	if err != nil {
		respondError(fmt.Sprintf("failed to resolve zone: %v", err))
		return
	}

	if zone == nil {
		resp := &coreProto.ResolveZoneResponse{Found: false}
		respData, _ := proto.Marshal(resp)
		_ = msg.Respond(respData)
		return
	}

	resp := &coreProto.ResolveZoneResponse{
		Found:  true,
		ZoneId: zone.ID.String(),
		Status: string(zone.Status),
		Name:   zone.Name,
	}
	respData, err := proto.Marshal(resp)
	if err != nil {
		respondError("failed to marshal ResolveZoneResponse")
		return
	}
	_ = msg.Respond(respData)
}
