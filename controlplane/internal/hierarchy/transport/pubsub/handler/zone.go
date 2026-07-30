package hierarchyPubsubHandler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyproto "controlplane/internal/hierarchy/transport/proto"
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
	requestIDSize          = 16
)

// [COMMENT]: ZoneRedisHandler phục vụ ACR bằng Shared Redis nội vùng Central.
// NATS Core được giữ cho realtime Central-Zone và không còn nằm trên lookup path ACR-Controlplane.
type ZoneRedisHandler struct {
	sharedRedis *goredis.Client
	zoneService hierarchySvcInterface.ZoneService
	otel        *observability.OTel

	cancel context.CancelFunc
	pubsub *goredis.PubSub
	loopWG sync.WaitGroup
	workWG sync.WaitGroup
	slots  chan struct{}
}

func NewZoneRedisHandler(
	sharedRedis *goredis.Client,
	zoneService hierarchySvcInterface.ZoneService,
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

						if value == nil {
							return
						}
						payload := []byte(value.Payload)
						// [COMMENT]: Giải mã request envelope gồm 16-byte request UUID ở đầu; Protobuf payload rỗng vẫn hợp lệ.
						if len(payload) < requestIDSize {
							return
						}
						requestID, err := uuid.FromBytes(payload[:requestIDSize])
						if err != nil || requestID == uuid.Nil {
							return
						}
						protobufPayload := payload[requestIDSize:]

						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						// [COMMENT]: Khóa distributed lock theo channel và request ID để tránh xử lý trùng lặp giữa các CP replica
						lockKey := "hierarchy:zone:dispatch:" + value.Channel + ":" + requestID.String()
						acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 15*time.Second).Result()
						if err != nil || !acquired {
							// [COMMENT]: PubSub fan-out tới mọi CP replica; chỉ SETNX winner được query PostgreSQL.
							return
						}

						var span trace.Span
						if h.otel != nil {
							ctx, span = h.otel.StartServerSpan(ctx, "Redis "+value.Channel)
							defer span.End()
							span.SetAttributes(
								attribute.String("messaging.system", "redis"),
								attribute.String("messaging.destination", value.Channel),
							)
						}

						switch value.Channel {
						case getZoneListChannel:
							h.ListZoneCatalog(ctx, requestID, protobufPayload)
						case resolveZoneChannel:
							h.ResolveZoneByCode(ctx, requestID, protobufPayload)
						}
					}(message)
				default:
					// [COMMENT]: Replica quá tải bỏ request; replica khác vẫn nhận broadcast.
					// Nếu toàn bộ cùng quá tải, ACR dùng bounded timeout rồi trả L1 snapshot
					// thay vì dồn DB vô hạn hoặc biến refresh failure thành HTTP 403.
				}
			}
		}
	}()
	return nil
}

func (h *ZoneRedisHandler) ListZoneCatalog(ctx context.Context, requestID uuid.UUID, payload []byte) {
	var request hierarchyproto.GetZoneListRequest
	if err := proto.Unmarshal(payload, &request); err != nil {
		return
	}
	zones, err := h.zoneService.ListZoneCatalog(ctx, &hierarchyEntity.ListZoneCatalog{})
	if err != nil {
		logger.HandlerErrorCtx(ctx, getZoneListChannel, err)
		return
	}
	wireZones := make([]*hierarchyproto.ZoneEntry, 0, len(zones))
	for _, zone := range zones {
		wireZones = append(wireZones, &hierarchyproto.ZoneEntry{
			ZoneId: zone.ID.String(), ZoneCode: zone.Code, Status: string(zone.Status), Name: zone.Name,
		})
	}
	response, err := proto.Marshal(&hierarchyproto.GetZoneListResponse{Zones: wireZones})
	if err != nil {
		return
	}
	_ = h.sharedRedis.Publish(ctx, getZoneListReplyPrefix+requestID.String(), response).Err()
}

func (h *ZoneRedisHandler) ResolveZoneByCode(ctx context.Context, requestID uuid.UUID, payload []byte) {
	var request hierarchyproto.ResolveZoneRequest
	if err := proto.Unmarshal(payload, &request); err != nil {
		return
	}
	zoneCode := strings.ToLower(strings.TrimSpace(request.ZoneCode))
	if zoneCode == "" || len(zoneCode) > 63 {
		return
	}
	zone, err := h.zoneService.ResolveZoneByCode(ctx, &hierarchyEntity.ResolveZoneByCode{Code: zoneCode})
	if err != nil {
		logger.HandlerErrorCtx(ctx, resolveZoneChannel, err)
		return
	}
	response := &hierarchyproto.ResolveZoneResponse{}
	if zone.Found {
		response.Found = true
		response.ZoneId = zone.ID.String()
		response.Status = string(zone.Status)
		response.Name = zone.Name
	}
	wire, err := proto.Marshal(response)
	if err != nil {
		return
	}
	_ = h.sharedRedis.Publish(ctx, resolveZoneReplyPrefix+requestID.String(), wire).Err()
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
