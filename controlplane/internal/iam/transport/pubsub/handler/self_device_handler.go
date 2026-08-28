package pubsubHandler

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
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
	service     iamSvcInterface.SelfDeviceService
	otel        *observability.OTel

	cancel context.CancelFunc
	pubsub *goredis.PubSub
	loopWG sync.WaitGroup
	workWG sync.WaitGroup
	slots  chan struct{}
}

func NewDevicePresenceProjectionRedisHandler(
	sharedRedis *goredis.Client,
	service iamSvcInterface.SelfDeviceService,
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
	if err := h.service.ApplyDevicePresenceProjection(ctx, updates); err != nil {
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

const (
	deviceSessionCapacityEvictionStream       = "iam:device:evicted-events"
	deviceSessionCapacityEvictionGroup        = "controlplane-device-eviction-v1"
	deviceSessionCapacityEvictionPayloadMax   = 64 * 1024
	deviceSessionCapacityEvictionBatchMax     = 64
	deviceSessionCapacityEvictionClaimMinIdle = 30 * time.Second
)

// DeviceSessionCapacityEvictionRedisHandler owns the durable ACR session-cap eviction workflow.
type DeviceSessionCapacityEvictionRedisHandler struct {
	sharedRedis *goredis.Client
	service     iamSvcInterface.SelfDeviceService
	otel        *observability.OTel

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewDeviceSessionCapacityEvictionRedisHandler(
	sharedRedis *goredis.Client,
	service iamSvcInterface.SelfDeviceService,
	otel *observability.OTel,
) *DeviceSessionCapacityEvictionRedisHandler {
	return &DeviceSessionCapacityEvictionRedisHandler{
		sharedRedis: sharedRedis,
		service:     service,
		otel:        otel,
	}
}

func (h *DeviceSessionCapacityEvictionRedisHandler) Start() error {
	ctx, cancel := context.WithCancel(pkgcontext.WithOperation(context.Background(), "iam.device.session_capacity_eviction.consume"))
	if err := h.sharedRedis.XGroupCreateMkStream(
		ctx,
		deviceSessionCapacityEvictionStream,
		deviceSessionCapacityEvictionGroup,
		"0",
	).Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		return fmt.Errorf("create device session-capacity eviction consumer group: %w", err)
	}
	h.cancel = cancel
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.run(ctx)
	}()
	return nil
}

func (h *DeviceSessionCapacityEvictionRedisHandler) run(ctx context.Context) {
	consumer := "iam-device-session-capacity-eviction-" + uuid.NewString()
	claimStart := "0-0"
	for ctx.Err() == nil {
		claimed, nextClaimStart, claimErr := h.sharedRedis.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
			Stream:   deviceSessionCapacityEvictionStream,
			Group:    deviceSessionCapacityEvictionGroup,
			Consumer: consumer,
			MinIdle:  deviceSessionCapacityEvictionClaimMinIdle,
			Start:    claimStart,
			Count:    deviceSessionCapacityEvictionBatchMax,
		}).Result()
		if claimErr != nil && !errors.Is(claimErr, goredis.Nil) {
			if ctx.Err() == nil {
				logger.SysWarnCtx(ctx, "iam.device.session_capacity_eviction.reclaim", claimErr.Error())
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(250*time.Millisecond + time.Duration(rand.IntN(251))*time.Millisecond):
			}
			continue
		}
		claimStart = nextClaimStart
		if claimStart == "" {
			claimStart = "0-0"
		}
		for _, message := range claimed {
			h.process(ctx, message)
		}

		streams, err := h.sharedRedis.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    deviceSessionCapacityEvictionGroup,
			Consumer: consumer,
			Streams:  []string{deviceSessionCapacityEvictionStream, ">"},
			Count:    deviceSessionCapacityEvictionBatchMax,
			Block:    5 * time.Second,
		}).Result()
		if err != nil {
			if !errors.Is(err, goredis.Nil) && ctx.Err() == nil {
				logger.SysWarnCtx(ctx, "iam.device.session_capacity_eviction.read", err.Error())
				select {
				case <-ctx.Done():
					return
				case <-time.After(250*time.Millisecond + time.Duration(rand.IntN(251))*time.Millisecond):
				}
			}
			continue
		}
		for _, stream := range streams {
			for _, message := range stream.Messages {
				h.process(ctx, message)
			}
		}
	}
}

func (h *DeviceSessionCapacityEvictionRedisHandler) process(ctx context.Context, message goredis.XMessage) {
	var payload []byte
	switch value := message.Values["payload"].(type) {
	case string:
		payload = []byte(value)
	case []byte:
		payload = value
	}
	if len(payload) == 0 || len(payload) > deviceSessionCapacityEvictionPayloadMax {
		logger.SysWarnCtx(ctx, "iam.device.session_capacity_eviction.invalid", "eviction stream payload is invalid")
		h.ack(ctx, message.ID)
		return
	}

	operationCtx, cancel := context.WithTimeout(
		pkgcontext.WithOperation(ctx, "iam.device.session_capacity_eviction.apply"),
		10*time.Second,
	)
	defer cancel()
	var span trace.Span
	if h.otel != nil {
		operationCtx, span = h.otel.StartServerSpan(operationCtx, "Redis iam.device.session_capacity_eviction")
		defer span.End()
		span.SetAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.destination", deviceSessionCapacityEvictionStream),
		)
	}

	var event iamproto.EvictedDevicesNotification
	if err := proto.Unmarshal(payload, &event); err != nil {
		logger.SysWarnCtx(operationCtx, "iam.device.session_capacity_eviction.invalid", "eviction stream payload is not protobuf")
		h.ack(operationCtx, message.ID)
		return
	}
	if len(event.ClientDeviceIds) == 0 || len(event.ClientDeviceIds) > deviceSessionCapacityEvictionBatchMax {
		logger.SysWarnCtx(operationCtx, "iam.device.session_capacity_eviction.invalid", "eviction device batch size is invalid")
		h.ack(operationCtx, message.ID)
		return
	}
	userID, err := uuid.Parse(strings.TrimSpace(event.UserId))
	if err != nil || userID == uuid.Nil {
		logger.SysWarnCtx(operationCtx, "iam.device.session_capacity_eviction.invalid", "eviction user ID is invalid")
		h.ack(operationCtx, message.ID)
		return
	}
	clientDeviceIDs := make([]uuid.UUID, 0, len(event.ClientDeviceIds))
	seen := make(map[uuid.UUID]struct{}, len(event.ClientDeviceIds))
	for _, rawDeviceID := range event.ClientDeviceIds {
		deviceID, err := uuid.Parse(strings.TrimSpace(rawDeviceID))
		if err != nil || deviceID == uuid.Nil {
			logger.SysWarnCtx(operationCtx, "iam.device.session_capacity_eviction.invalid", "eviction client device ID is invalid")
			h.ack(operationCtx, message.ID)
			return
		}
		if _, duplicate := seen[deviceID]; duplicate {
			continue
		}
		seen[deviceID] = struct{}{}
		clientDeviceIDs = append(clientDeviceIDs, deviceID)
	}
	if err := h.service.ApplyDeviceSessionCapacityEviction(operationCtx, userID, clientDeviceIDs); err != nil {
		logger.SysErrorCtx(operationCtx, "iam.device.session_capacity_eviction.apply", err.Error())
		return
	}
	h.ack(operationCtx, message.ID)
}

func (h *DeviceSessionCapacityEvictionRedisHandler) ack(ctx context.Context, messageID string) {
	if err := h.sharedRedis.XAck(
		ctx,
		deviceSessionCapacityEvictionStream,
		deviceSessionCapacityEvictionGroup,
		messageID,
	).Err(); err != nil {
		logger.SysErrorCtx(ctx, "iam.device.session_capacity_eviction.ack", err.Error())
		return
	}
	if err := h.sharedRedis.XDel(ctx, deviceSessionCapacityEvictionStream, messageID).Err(); err != nil {
		logger.SysErrorCtx(ctx, "iam.device.session_capacity_eviction.delete", err.Error())
	}
}

func (h *DeviceSessionCapacityEvictionRedisHandler) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	h.wg.Wait()
}
