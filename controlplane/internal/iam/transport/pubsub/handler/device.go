package pubsubHandler

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/observability"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: DeviceNatsHandler quản lý các NATS subscription liên quan đến nghiệp vụ Device (bao gồm bulk presence)
type DeviceNatsHandler struct {
	cfg       *config.Config
	deviceSvc iamSvcInterface.DeviceSelfService
	otel      *observability.OTel
}

// [COMMENT]: NewDeviceNatsHandler khởi tạo handler lắng nghe các sự kiện NATS cho Device domain
func NewDeviceNatsHandler(
	cfg *config.Config,
	deviceSvc iamSvcInterface.DeviceSelfService,
	otel *observability.OTel,
) *DeviceNatsHandler {
	return &DeviceNatsHandler{
		cfg:       cfg,
		deviceSvc: deviceSvc,
		otel:      otel,
	}
}

// [COMMENT]: Subscribe đăng ký lắng nghe bulk presence updates qua NATS Core.
func (h *DeviceNatsHandler) Subscribe(nc *nats.Conn) ([]*nats.Subscription, error) {
	const queueGroup = "iam_device_service"
	var subs []*nats.Subscription

	subBulkPresence, err := nc.QueueSubscribe("iam.device.bulk_touch_presence", queueGroup, func(msg *nats.Msg) {
		ctx := context.Background()

		if msg.Header != nil {
			traceparent := msg.Header.Get("traceparent")
			if traceparent != "" {
				ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
			}
		}

		var span trace.Span
		if h.otel != nil {
			ctx, span = h.otel.StartServerSpan(ctx, "NATS iam.device.bulk_touch_presence")
			defer span.End()
			span.SetAttributes(
				attribute.String("messaging.system", "nats"),
				attribute.String("messaging.destination", "iam.device.bulk_touch_presence"),
			)
		}

		// Giải mã payload Protobuf
		var req iamproto.BulkTouchDevicesRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			logger.SysError("NATS.BulkTouchPresence", fmt.Sprintf("Failed to unmarshal request: %v", err))
			return
		}

		if len(req.Updates) == 0 {
			return
		}

		// Map sang entity updates
		updates := make([]iamEntity.DevicePresenceUpdate, len(req.Updates))
		for i, u := range req.Updates {
			updates[i] = iamEntity.DevicePresenceUpdate{
				DeviceID:          u.DeviceId,
				LastSeenAt:        u.LastSeenAt,
				LastSeenIP:        u.LastSeenIp,
				LastSeenUserAgent: u.LastSeenUserAgent,
			}
		}

		// Thực thi bulk upsert vào DB
		if err := h.deviceSvc.BulkTouchDevices(ctx, updates); err != nil {
			logger.SysError("NATS.BulkTouchPresence", fmt.Sprintf("Failed to bulk touch devices: %v", err))
			return
		}
	})

	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to iam.device.bulk_touch_presence: %w", err)
	}
	subs = append(subs, subBulkPresence)

	// [COMMENT]: Đăng ký lắng nghe sự kiện thu hồi thiết bị do vượt quá dung lượng (evicted) từ ACR
	subEvicted, err := nc.QueueSubscribe("iam.device.evicted", queueGroup, func(msg *nats.Msg) {
		ctx := context.Background()

		if msg.Header != nil {
			traceparent := msg.Header.Get("traceparent")
			if traceparent != "" {
				ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
			}
		}

		var span trace.Span
		if h.otel != nil {
			ctx, span = h.otel.StartServerSpan(ctx, "NATS iam.device.evicted")
			defer span.End()
			span.SetAttributes(
				attribute.String("messaging.system", "nats"),
				attribute.String("messaging.destination", "iam.device.evicted"),
			)
		}

		var req iamproto.EvictedDevicesNotification
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			logger.SysError("NATS.EvictedDevices", fmt.Sprintf("Failed to unmarshal request: %v", err))
			return
		}

		if len(req.ClientDeviceIds) == 0 {
			return
		}

		userUUID, err := uuid.Parse(req.UserId)
		if err != nil {
			logger.SysError("NATS.EvictedDevices", fmt.Sprintf("Invalid user UUID: %s", req.UserId))
			return
		}

		// Thực thi thu hồi thiết bị và session tương ứng trong database
		if err := h.deviceSvc.RevokeDevicesByClientDeviceIDs(ctx, userUUID, req.ClientDeviceIds); err != nil {
			logger.SysError("NATS.EvictedDevices", fmt.Sprintf("Failed to revoke evicted devices: %v", err))
			return
		}

		logger.SysInfo("NATS.EvictedDevices", fmt.Sprintf("Successfully revoked %d evicted devices for user_id=%s", len(req.ClientDeviceIds), req.UserId))
	})

	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to iam.device.evicted: %w", err)
	}
	subs = append(subs, subEvicted)

	return subs, nil
}
