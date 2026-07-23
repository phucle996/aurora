package pubsubHandler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	coreSvcInterface "controlplane/internal/hierarchy/domain/service"
	coreProto "controlplane/internal/hierarchy/transport/proto"
	"controlplane/internal/observability"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const (
	getZoneListChannel     = "hierarchy.zone.get_zone_list"
	getZoneListReplyPrefix = "hierarchy.zone.get_zone_list.reply."
	resolveZoneChannel     = "hierarchy.zone.resolve_zone"
	resolveZoneReplyPrefix = "hierarchy.zone.resolve_zone.reply."
)

// [COMMENT]: ZoneRedisHandler phục vụ ACR bằng Shared Redis nội vùng Central.
// NATS Core được giữ cho realtime Central-Zone và không còn nằm trên lookup path ACR-Controlplane.
type ZoneRedisHandler struct {
	sharedRedis *goredis.Client
	zoneService coreSvcInterface.ZoneService
	otel        *observability.OTel

	cancel context.CancelFunc
	pubsub *goredis.PubSub
	loopWG sync.WaitGroup
	workWG sync.WaitGroup
	slots  chan struct{}
}

func NewZoneRedisHandler(
	sharedRedis *goredis.Client,
	zoneService coreSvcInterface.ZoneService,
	otel *observability.OTel,
) (*ZoneRedisHandler, error) {
	if sharedRedis == nil || zoneService == nil {
		return nil, errors.New("zone Redis handler requires Shared Redis and ZoneService")
	}
	return &ZoneRedisHandler{
		sharedRedis: sharedRedis,
		zoneService: zoneService,
		otel:        otel,
		slots:       make(chan struct{}, 32),
	}, nil
}

func (h *ZoneRedisHandler) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	pubsub := h.sharedRedis.Subscribe(ctx, getZoneListChannel, resolveZoneChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		cancel()
		_ = pubsub.Close()
		return fmt.Errorf("subscribe zone Redis channels: %w", err)
	}
	h.cancel = cancel
	h.pubsub = pubsub

	h.loopWG.Add(1)
	go func() {
		defer h.loopWG.Done()
		channel := pubsub.Channel(goredis.WithChannelSize(128))
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
					go func(value *goredis.Message) {
						defer h.workWG.Done()
						defer func() { <-h.slots }()
						h.dispatch(value)
					}(message)
				default:
					// [COMMENT]: Replica quá tải bỏ request; replica khác vẫn nhận broadcast.
					// Nếu toàn bộ cùng quá tải, ACR timeout fail-close thay vì dồn DB vô hạn.
				}
			}
		}
	}()
	return nil
}

func (h *ZoneRedisHandler) dispatch(message *goredis.Message) {
	if message == nil {
		return
	}
	payload := []byte(message.Payload)
	if len(payload) <= 16 {
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lockKey := "hierarchy:zone:dispatch:" + message.Channel + ":" + requestID.String()
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 15*time.Second).Result()
	if err != nil || !acquired {
		// [COMMENT]: PubSub fan-out tới mọi CP replica; chỉ SETNX winner được query PostgreSQL.
		return
	}

	var span trace.Span
	if h.otel != nil {
		ctx, span = h.otel.StartServerSpan(ctx, "Redis "+message.Channel)
		defer span.End()
		span.SetAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.destination", message.Channel),
		)
	}

	switch message.Channel {
	case getZoneListChannel:
		var request coreProto.GetZoneListRequest
		if err := proto.Unmarshal(payload[16:], &request); err != nil {
			return
		}
		zones, err := h.zoneService.AcrListZones(ctx)
		if err != nil {
			logger.HandlerErrorCtx(ctx, getZoneListChannel, err)
			return
		}
		wireZones := make([]*coreProto.ZoneEntry, 0, len(zones))
		for _, zone := range zones {
			wireZones = append(wireZones, &coreProto.ZoneEntry{
				ZoneId:   zone.ID.String(),
				ZoneCode: zone.Code,
				Status:   string(zone.Status),
				Name:     zone.Name,
			})
		}
		response, err := proto.Marshal(&coreProto.GetZoneListResponse{Zones: wireZones})
		if err == nil {
			_ = h.sharedRedis.Publish(ctx, getZoneListReplyPrefix+requestID.String(), response).Err()
		}
	case resolveZoneChannel:
		var request coreProto.ResolveZoneRequest
		if err := proto.Unmarshal(payload[16:], &request); err != nil {
			return
		}
		zone, err := h.zoneService.AcrResolveZone(ctx, request.ZoneCode)
		if err != nil {
			logger.HandlerErrorCtx(ctx, resolveZoneChannel, err)
			return
		}
		response := &coreProto.ResolveZoneResponse{}
		if zone != nil {
			response.Found = true
			response.ZoneId = zone.ID.String()
			response.Status = string(zone.Status)
			response.Name = zone.Name
		}
		wire, err := proto.Marshal(response)
		if err == nil {
			_ = h.sharedRedis.Publish(ctx, resolveZoneReplyPrefix+requestID.String(), wire).Err()
		}
	}
}

func (h *ZoneRedisHandler) Stop() {
	if h == nil {
		return
	}
	if h.cancel != nil {
		h.cancel()
	}
	if h.pubsub != nil {
		_ = h.pubsub.Close()
	}
	h.loopWG.Wait()
	h.workWG.Wait()
}
