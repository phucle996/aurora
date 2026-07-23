package pubsubHandler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/observability"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const (
	bulkTouchPresenceChannel = "iam.device.bulk_touch_presence"
	evictedDevicesStream     = "iam:device:evicted-events"
	evictedDevicesGroup      = "controlplane-device-eviction-v1"
	evictedDevicesConsumer   = "controlplane-device-eviction"
)

// [COMMENT]: DeviceRedisHandler quản lý các Shared Redis PubSub subscription liên quan đến nghiệp vụ Device (bao gồm bulk presence)
type DeviceRedisHandler struct {
	cfg         *config.Config
	sharedRedis *goredis.Client
	deviceSvc   iamSvcInterface.DeviceSelfService
	otel        *observability.OTel

	cancel context.CancelFunc
	pubsub *goredis.PubSub
	loopWG sync.WaitGroup
	workWG sync.WaitGroup
	slots  chan struct{}
}

// [COMMENT]: NewDeviceRedisHandler khởi tạo handler lắng nghe các sự kiện qua Shared Redis PubSub cho Device domain
func NewDeviceRedisHandler(
	cfg *config.Config,
	sharedRedis *goredis.Client,
	deviceSvc iamSvcInterface.DeviceSelfService,
	otel *observability.OTel,
) (*DeviceRedisHandler, error) {
	if sharedRedis == nil || deviceSvc == nil {
		return nil, errors.New("device Redis handler requires Shared Redis and DeviceSelfService")
	}
	return &DeviceRedisHandler{
		cfg:         cfg,
		sharedRedis: sharedRedis,
		deviceSvc:   deviceSvc,
		otel:        otel,
		slots:       make(chan struct{}, 64),
	}, nil
}

// [COMMENT]: Start bắt đầu đăng ký các channel Device qua Shared Redis PubSub.
func (h *DeviceRedisHandler) Start() error {
	if h == nil {
		return errors.New("device Redis handler is nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	pubsub := h.sharedRedis.Subscribe(ctx, bulkTouchPresenceChannel)
	if _, err := pubsub.Receive(ctx); err != nil {
		cancel()
		_ = pubsub.Close()
		return fmt.Errorf("subscribe Device Redis channels: %w", err)
	}
	h.cancel = cancel
	h.pubsub = pubsub
	if err := h.sharedRedis.XGroupCreateMkStream(ctx, evictedDevicesStream, evictedDevicesGroup, "0").Err(); err != nil &&
		!strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		_ = pubsub.Close()
		return fmt.Errorf("create device eviction consumer group: %w", err)
	}

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
					go func(msg *goredis.Message) {
						defer h.workWG.Done()
						defer func() { <-h.slots }()
						h.dispatch(msg)
					}(message)
				default:
					// [COMMENT]: CP replica quá tải sẽ skip message mà không làm nghẽn DB
				}
			}
		}
	}()

	h.loopWG.Add(1)
	go func() {
		defer h.loopWG.Done()
		for {
			if ctx.Err() != nil {
				return
			}

			// [COMMENT]: Mọi CP replica dùng cùng consumer identity. Đọc pending ID=0
			// trước giúp replica còn sống tiếp quản entry của pod vừa chết.
			messages, err := h.sharedRedis.XReadGroup(ctx, &goredis.XReadGroupArgs{
				Group:    evictedDevicesGroup,
				Consumer: evictedDevicesConsumer,
				Streams:  []string{evictedDevicesStream, "0"},
				Count:    32,
			}).Result()
			if err != nil && !errors.Is(err, goredis.Nil) {
				if ctx.Err() != nil {
					return
				}
				logger.SysError("Redis.EvictedDevices", fmt.Sprintf("Failed to read pending events: %v", err))
				time.Sleep(500 * time.Millisecond)
				continue
			}
			if len(messages) == 0 {
				messages, err = h.sharedRedis.XReadGroup(ctx, &goredis.XReadGroupArgs{
					Group:    evictedDevicesGroup,
					Consumer: evictedDevicesConsumer,
					Streams:  []string{evictedDevicesStream, ">"},
					Count:    32,
					Block:    5 * time.Second,
				}).Result()
				if err != nil {
					if errors.Is(err, goredis.Nil) {
						continue
					}
					if ctx.Err() != nil {
						return
					}
					logger.SysError("Redis.EvictedDevices", fmt.Sprintf("Failed to read new events: %v", err))
					time.Sleep(500 * time.Millisecond)
					continue
				}
			}

			for _, stream := range messages {
				for _, message := range stream.Messages {
					// [COMMENT]: Shared consumer identity cho phép HA takeover nhưng nhiều
					// replica có thể cùng nhìn pending entry; lock theo stream ID chặn duplicate DB write.
					acquired, lockErr := h.sharedRedis.SetNX(
						ctx,
						"iam:device:dispatch:evicted-stream:"+message.ID,
						"1",
						10*time.Second,
					).Result()
					if lockErr != nil || !acquired {
						time.Sleep(250 * time.Millisecond)
						continue
					}
					payload, ok := message.Values["payload"].(string)
					if !ok {
						// [COMMENT]: go-redis trả binary field dưới dạng string; thiếu field là
						// poison event nên ACK+DEL để không khóa toàn consumer group.
						_, _ = h.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
							pipe.XAck(ctx, evictedDevicesStream, evictedDevicesGroup, message.ID)
							pipe.XDel(ctx, evictedDevicesStream, message.ID)
							return nil
						})
						continue
					}
					if !h.handleEvictedDevices([]byte(payload)) {
						time.Sleep(500 * time.Millisecond)
						continue
					}
					_, _ = h.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
						pipe.XAck(ctx, evictedDevicesStream, evictedDevicesGroup, message.ID)
						pipe.XDel(ctx, evictedDevicesStream, message.ID)
						return nil
					})
				}
			}
		}
	}()
	return nil
}

func (h *DeviceRedisHandler) dispatch(msg *goredis.Message) {
	if msg == nil {
		return
	}
	payload := []byte(msg.Payload)
	switch msg.Channel {
	case bulkTouchPresenceChannel:
		h.handleBulkTouchPresence(payload)
	}
}

// =========================================================================
// 1. LUỒNG CẬP NHẬT BULK PRESENCE (BulkTouchPresence)
// =========================================================================
func (h *DeviceRedisHandler) handleBulkTouchPresence(payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var span trace.Span
	if h.otel != nil {
		ctx, span = h.otel.StartServerSpan(ctx, "Redis iam.device.bulk_touch_presence")
		defer span.End()
		span.SetAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.destination", bulkTouchPresenceChannel),
		)
	}

	// [COMMENT]: PubSub broadcast tới mọi CP replica; event_id 16 byte và SETNX biến
	// fan-out thành single-consumer trước khi thực hiện bulk UPDATE PostgreSQL.
	if len(payload) <= 16 {
		logger.SysWarn("Redis.BulkTouchPresence", "Missing event id envelope")
		return
	}
	eventID, err := uuid.FromBytes(payload[:16])
	if err != nil || eventID == uuid.Nil {
		logger.SysWarn("Redis.BulkTouchPresence", "Invalid event id envelope")
		return
	}
	acquired, err := h.sharedRedis.SetNX(ctx, "iam:device:dispatch:bulk_touch:"+eventID.String(), "1", 2*time.Minute).Result()
	if err != nil || !acquired {
		return
	}

	var req iamproto.BulkTouchDevicesRequest
	if err := proto.Unmarshal(payload[16:], &req); err != nil {
		logger.SysError("Redis.BulkTouchPresence", fmt.Sprintf("Failed to unmarshal request: %v", err))
		return
	}

	if len(req.Updates) == 0 {
		return
	}

	updates := make([]iamEntity.DevicePresenceUpdate, len(req.Updates))
	for i, u := range req.Updates {
		updates[i] = iamEntity.DevicePresenceUpdate{
			DeviceID:          u.DeviceId,
			LastSeenAt:        u.LastSeenAt,
			LastSeenIP:        u.LastSeenIp,
			LastSeenUserAgent: u.LastSeenUserAgent,
		}
	}

	if err := h.deviceSvc.BulkTouchDevices(ctx, updates); err != nil {
		logger.SysError("Redis.BulkTouchPresence", fmt.Sprintf("Failed to bulk touch devices: %v", err))
		return
	}
}

// =========================================================================
// 2. LUỒNG THU HỒI THIẾT BỊ THỪA DUNG LƯỢNG (EvictedDevices)
// =========================================================================
func (h *DeviceRedisHandler) handleEvictedDevices(payload []byte) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var span trace.Span
	if h.otel != nil {
		ctx, span = h.otel.StartServerSpan(ctx, "Redis iam.device.evicted")
		defer span.End()
		span.SetAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.destination", evictedDevicesStream),
		)
	}

	var req iamproto.EvictedDevicesNotification
	if err := proto.Unmarshal(payload, &req); err != nil {
		logger.SysError("Redis.EvictedDevices", fmt.Sprintf("Failed to unmarshal request: %v", err))
		return true
	}

	if len(req.ClientDeviceIds) == 0 {
		return true
	}

	userUUID, err := uuid.Parse(req.UserId)
	if err != nil {
		logger.SysError("Redis.EvictedDevices", fmt.Sprintf("Invalid user UUID: %s", req.UserId))
		return true
	}

	if err := h.deviceSvc.EvictDevices(ctx, userUUID, req.ClientDeviceIds); err != nil {
		logger.SysError("Redis.EvictDevices", fmt.Sprintf("Failed to evict devices: %v", err))
		return false
	}

	logger.SysInfo("Redis.EvictDevices", fmt.Sprintf("Successfully evicted %d evicted devices for user_id=%s", len(req.ClientDeviceIds), req.UserId))
	return true
}

func (h *DeviceRedisHandler) Stop() {
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
