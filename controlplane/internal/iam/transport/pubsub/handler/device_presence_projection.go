package pubsubHandler

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	"controlplane/internal/observability"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	iamproto "controlplane/internal/iam/transport/proto"
)

const (
	devicePresenceChannel         = "iam.device.bulk_touch_presence"
	devicePresencePayloadMaxBytes = 256 * 1024
	devicePresenceBatchMax        = 1_024
	devicePresenceUserAgentMax    = 1_024
	devicePresenceFutureSkew      = 5 * time.Minute
	devicePresenceMaximumAge      = 366 * 24 * time.Hour
)

// DevicePresenceProjectionRedisHandler owns the lossy, advisory presence projection.
type DevicePresenceProjectionRedisHandler struct {
	sharedRedis *goredis.Client
	service     iamSvcInterface.DevicePresenceProjectionService
	otel        *observability.OTel

	cancel context.CancelFunc
	pubsub *goredis.PubSub
	loopWG sync.WaitGroup
	workWG sync.WaitGroup
	slots  chan struct{}
}

func NewDevicePresenceProjectionRedisHandler(
	sharedRedis *goredis.Client,
	service iamSvcInterface.DevicePresenceProjectionService,
	otel *observability.OTel,
) *DevicePresenceProjectionRedisHandler {
	return &DevicePresenceProjectionRedisHandler{
		sharedRedis: sharedRedis,
		service:     service,
		otel:        otel,
		slots:       make(chan struct{}, 64),
	}
}

func (h *DevicePresenceProjectionRedisHandler) Start() error {
	ctx, cancel := context.WithCancel(pkgcontext.WithOperation(context.Background(), "iam.device.presence.subscribe"))
	pubsub := h.sharedRedis.Subscribe(ctx, devicePresenceChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		cancel()
		_ = pubsub.Close()
		return fmt.Errorf("subscribe device presence: %w", err)
	}
	h.cancel = cancel
	h.pubsub = pubsub

	h.loopWG.Add(1)
	go func() {
		defer h.loopWG.Done()
		channel := pubsub.Channel(goredis.WithChannelSize(256))
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-channel:
				if !ok {
					return
				}
				select {
				case h.slots <- struct{}{}:
					h.workWG.Add(1)
					go func(payload []byte) {
						defer h.workWG.Done()
						defer func() { <-h.slots }()
						h.handle(payload)
					}([]byte(message.Payload))
				default:
					logger.SysWarnCtx(ctx, "iam.device.presence.drop", "presence batch dropped because every local dispatch slot is busy")
				}
			}
		}
	}()
	return nil
}

func (h *DevicePresenceProjectionRedisHandler) handle(payload []byte) {
	ctx, cancel := context.WithTimeout(
		pkgcontext.WithOperation(context.Background(), "iam.device.presence.apply"),
		10*time.Second,
	)
	defer cancel()

	var span trace.Span
	if h.otel != nil {
		ctx, span = h.otel.StartServerSpan(ctx, "Redis iam.device.presence")
		defer span.End()
		span.SetAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.destination", devicePresenceChannel),
		)
	}

	if len(payload) <= 16 || len(payload) > devicePresencePayloadMaxBytes+16 {
		logger.SysWarnCtx(ctx, "iam.device.presence.invalid", "presence envelope size is invalid")
		return
	}
	eventID, err := uuid.FromBytes(payload[:16])
	if err != nil || eventID == uuid.Nil {
		logger.SysWarnCtx(ctx, "iam.device.presence.invalid", "presence event ID is invalid")
		return
	}

	var request iamproto.BulkTouchDevicesRequest
	if err := proto.Unmarshal(payload[16:], &request); err != nil {
		logger.SysWarnCtx(ctx, "iam.device.presence.invalid", "presence payload is not protobuf")
		return
	}
	if len(request.Updates) == 0 || len(request.Updates) > devicePresenceBatchMax {
		logger.SysWarnCtx(ctx, "iam.device.presence.invalid", "presence batch size is invalid")
		return
	}

	now := time.Now().UTC()
	updates := make([]iamEntity.DevicePresenceUpdate, 0, len(request.Updates))
	for _, update := range request.Updates {
		if update == nil {
			continue
		}
		deviceID, err := uuid.Parse(strings.TrimSpace(update.DeviceId))
		if err != nil || deviceID == uuid.Nil {
			continue
		}
		lastSeenAt := time.Unix(update.LastSeenAt, 0).UTC()
		if update.LastSeenAt <= 0 || lastSeenAt.Before(now.Add(-devicePresenceMaximumAge)) || lastSeenAt.After(now.Add(devicePresenceFutureSkew)) {
			continue
		}
		ip := strings.TrimSpace(update.LastSeenIp)
		if ip != "" && net.ParseIP(ip) == nil {
			continue
		}
		userAgent := strings.TrimSpace(update.LastSeenUserAgent)
		if len(userAgent) > devicePresenceUserAgentMax || !utf8.ValidString(userAgent) {
			continue
		}
		updates = append(updates, iamEntity.DevicePresenceUpdate{
			DeviceID:          deviceID.String(),
			LastSeenAt:        update.LastSeenAt,
			LastSeenIP:        ip,
			LastSeenUserAgent: userAgent,
		})
	}
	if len(updates) == 0 {
		logger.SysWarnCtx(ctx, "iam.device.presence.invalid", "presence batch has no valid updates")
		return
	}

	acquired, err := h.sharedRedis.SetNX(
		ctx,
		"iam:device:dispatch:presence:"+eventID.String(),
		"1",
		2*time.Minute,
	).Result()
	if err != nil || !acquired {
		return
	}
	if err := h.service.Apply(ctx, updates); err != nil {
		logger.SysErrorCtx(ctx, "iam.device.presence.apply", err.Error())
	}
}

func (h *DevicePresenceProjectionRedisHandler) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	if h.pubsub != nil {
		_ = h.pubsub.Close()
	}
	h.loopWG.Wait()
	h.workWG.Wait()
}
