package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cost-manager/api/internal/config"
	"cost-manager/api/internal/domain/entity"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	ownershipv1 "cost-manager/api/internal/genproto/billing/ownership/v1"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// HẰNG SỐ CẤU HÌNH REDIS STREAM VÀ THỜI GIAN LEASE
// ============================================================================
const (
	// Stream chính nhận sự kiện thay đổi chủ sở hữu tài nguyên từ Job Orchestrator
	resourceOwnershipStream = "stream:{billing}:resource_ownership"

	// Tên Consumer Group của Cost Manager phục vụ phân phối tin nhắn cân bằng tải
	resourceOwnershipGroup = "cost-resource-ownership-v1"

	// Stream Dead-Letter Queue lưu trữ các bản ghi vi phạm hợp đồng (corrupted / invalid)
	resourceOwnershipDLQ = "stream:{billing}:resource_ownership:dlq"

	// Thời gian chờ tối thiểu (30s) trước khi một pod khác được phép reclaim message bị treo
	resourceOwnershipReclaim = 30 * time.Second

	// Số lượng tin nhắn tối đa đọc trong một đợt (batch)
	resourceOwnershipBatch = int64(32)

	// Giới hạn kích thước tối đa của một payload protobuf (64 KB) để chống tấn công cạn kiệt bộ nhớ
	resourceOwnershipMaxSize = 64 * 1024

	// Kích thước tối đa của DLQ Stream trước khi bắt đầu drop message cũ (Approximate MaxLen)
	resourceOwnershipDLQSize = int64(10_000)
)

// ============================================================================
// RESOURCE OWNERSHIP CONSUMER - BỘ TIẾP NHẬN SỰ KIỆN SỞ HỮU TÀI NGUYÊN
// ============================================================================
//
// Vai trò kiến trúc:
// - Lắng nghe các sự kiện `ResourceOwnershipChangedV1` từ Job Orchestrator phát qua Shared Redis Stream.
// - Xác thực hợp đồng dữ liệu nghiêm ngặt trước khi chuyển giao cho Billing Service Layer.
// - Quản lý chu trình xác nhận (XACK / XDEL) và tự động nhận lại các message bị rớt (XAUTOCLAIM) khi pod crash.
type ResourceOwnershipConsumer struct {
	sharedRedis *goredis.Client
	ownership   billingSvcInterface.ResourceOwnershipService
	consumer    string

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// NewResourceOwnershipConsumer khởi tạo Consumer với định danh Consumer Name độc nhất dựa trên Node Hostname toàn cục + UUID ngẫu nhiên.
func NewResourceOwnershipConsumer(
	sharedRedis *goredis.Client,
	ownership billingSvcInterface.ResourceOwnershipService,
) *ResourceOwnershipConsumer {
	return &ResourceOwnershipConsumer{
		sharedRedis: sharedRedis,
		ownership:   ownership,
		consumer:    config.GetNodeHostname() + "-" + uuid.NewString(),
		done:        make(chan struct{}),
	}
}

// Start khởi động Consumer: Tạo Consumer Group (idempotent) và chạy vòng lặp xử lý nền.
func (s *ResourceOwnershipConsumer) Start() error {
	if s == nil {
		return errors.New("resource ownership consumer is nil")
	}
	if s.cancel != nil {
		return errors.New("resource ownership consumer already started")
	}
	ctx, cancel := context.WithCancel(context.Background())

	// Tạo Consumer Group nếu chưa tồn tại (bỏ qua lỗi BUSYGROUP nếu đã được tạo từ trước)
	if err := s.sharedRedis.XGroupCreateMkStream(
		ctx,
		resourceOwnershipStream,
		resourceOwnershipGroup,
		"0",
	).Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		return fmt.Errorf("create resource ownership consumer group: %w", err)
	}
	s.cancel = cancel
	go s.run(ctx)
	return nil
}

// run duy trì vòng lặp chính tiếp nhận sự kiện: Kết hợp XAutoClaim (nhặt tin nhắn cũ bị treo) và XReadGroup (đọc tin mới).
func (s *ResourceOwnershipConsumer) run(ctx context.Context) {
	defer close(s.done)
	claimCursor := "0-0"
	for ctx.Err() == nil {
		// ─────────────────────────────────────────────────────────────────────
		// BƯỚC 1: XAUTOCLAIM - Nhặt lại các message đang Pending quá 30 giây
		// (Do pod trước đó bị sập hoặc mất kết nối mạng đột ngột)
		// ─────────────────────────────────────────────────────────────────────
		claimed, nextCursor, err := s.sharedRedis.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
			Stream:   resourceOwnershipStream,
			Group:    resourceOwnershipGroup,
			Consumer: s.consumer,
			MinIdle:  resourceOwnershipReclaim,
			Start:    claimCursor,
			Count:    resourceOwnershipBatch,
		}).Result()
		if err != nil && !errors.Is(err, goredis.Nil) {
			if ctx.Err() != nil {
				return
			}
			logger.SysError("billing.ownership.redis.reclaim", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}

		claimCursor = nextCursor
		for _, message := range claimed {
			s.process(ctx, message)
		}
		// Nếu vừa claim được tin nhắn, tiếp tục vòng lặp để xử lý dứt điểm các tin nhắn pending
		if len(claimed) > 0 {
			continue
		}
		if claimCursor != "0-0" {
			continue
		}

		// ─────────────────────────────────────────────────────────────────────
		// BƯỚC 2: XREADGROUP - Đọc các tin nhắn mới phát sinh (kí hiệu '>')
		// Block tối đa 5 giây chờ đợi có message mới
		// ─────────────────────────────────────────────────────────────────────
		streams, err := s.sharedRedis.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    resourceOwnershipGroup,
			Consumer: s.consumer,
			Streams:  []string{resourceOwnershipStream, ">"},
			Count:    resourceOwnershipBatch,
			Block:    5 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			logger.SysError("billing.ownership.redis.read", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		for _, stream := range streams {
			for _, message := range stream.Messages {
				s.process(ctx, message)
			}
		}
	}
}

// process xử lý từng message nhận được từ Redis Stream và chuyển giao xuống Service Layer.
func (s *ResourceOwnershipConsumer) process(ctx context.Context, message goredis.XMessage) {
	var eventIDText string
	switch v := message.Values["event_id"].(type) {
	case string:
		eventIDText = v
	case []byte:
		eventIDText = string(v)
	}

	var eventTypeText string
	switch v := message.Values["event_type"].(type) {
	case string:
		eventTypeText = v
	case []byte:
		eventTypeText = string(v)
	}

	var payload []byte
	switch v := message.Values["payload"].(type) {
	case []byte:
		payload = v
	case string:
		payload = []byte(v)
	}

	// 1. Kiểm tra giới hạn dung lượng payload
	if len(payload) == 0 || len(payload) > resourceOwnershipMaxSize {
		s.deadLetter(ctx, message, "invalid_payload_size")
		return
	}

	// 2. Giải mã (Unmarshal) Protobuf
	wire := &ownershipv1.ResourceOwnershipChangedV1{}
	protoErr := proto.Unmarshal(payload, wire)
	eventID, eventErr := uuid.Parse(eventIDText)
	wireEventID, wireEventErr := uuid.FromBytes(wire.GetEventId())
	resourceID, resourceErr := uuid.FromBytes(wire.GetResourceId())
	ownerID, ownerErr := uuid.FromBytes(wire.GetOwnerId())
	zoneID, zoneErr := uuid.FromBytes(wire.GetZoneId())
	sourceJobID, sourceJobErr := uuid.FromBytes(wire.GetSourceJobId())
	effectiveAt, timeErr := time.Parse(time.RFC3339Nano, wire.GetEffectiveAt())

	eventType := entity.ResourceOwnershipEventType(wire.GetEventType())
	validEventType := eventType == entity.ResourceOwnershipEventCreated || eventType == entity.ResourceOwnershipEventDeleted
	validOwnerType := wire.GetOwnerType() == "PERSONAL" || wire.GetOwnerType() == "TENANT"
	validResourceType := wire.GetResourceType() == "STORAGE_BUCKET" ||
		wire.GetResourceType() == "HYPERVISOR_VM" ||
		wire.GetResourceType() == "MAIL_CONSUMER"

	// Inline kiểm tra định dạng W3C Traceparent
	validTraceparent := true
	if tp := wire.GetTraceparent(); tp != "" {
		parts := strings.Split(tp, "-")
		if len(parts) != 4 || len(parts[0]) != 2 || parts[0] == "ff" ||
			len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
			validTraceparent = false
		} else {
			version, vErr := hex.DecodeString(parts[0])
			traceID, tErr := hex.DecodeString(parts[1])
			spanID, sErr := hex.DecodeString(parts[2])
			flags, fErr := hex.DecodeString(parts[3])
			if vErr != nil || tErr != nil || sErr != nil || fErr != nil || len(version) != 1 || len(flags) != 1 {
				validTraceparent = false
			} else {
				traceZero, spanZero := true, true
				for _, b := range traceID {
					if b != 0 {
						traceZero = false
						break
					}
				}
				for _, b := range spanID {
					if b != 0 {
						spanZero = false
						break
					}
				}
				if traceZero || spanZero {
					validTraceparent = false
				}
			}
		}
	}

	// =========================================================================
	// HÀNG RÀO HỢP ĐỒNG (CONTRACT BOUNDARY GUARDS)
	// =========================================================================

	// Guard 1: Tính toàn vẹn của Header & Message Envelope
	if eventErr != nil || protoErr != nil || wireEventErr != nil ||
		eventID == uuid.Nil || wireEventID != eventID || eventTypeText != wire.GetEventType() {
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}

	// Guard 2: Tính hợp lệ của các định danh tài nguyên & chủ sở hữu
	if resourceErr != nil || resourceID == uuid.Nil ||
		ownerErr != nil || ownerID == uuid.Nil ||
		zoneErr != nil || zoneID == uuid.Nil ||
		sourceJobErr != nil || sourceJobID == uuid.Nil {
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}

	// Guard 3: Schema version và thời gian hiệu lực
	if wire.GetSchemaVersion() != 1 || wire.GetSourceVersion() <= 0 || timeErr != nil {
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}

	// Guard 4: Phân loại tài nguyên và ràng buộc tên
	if !validEventType || !validOwnerType || !validResourceType ||
		strings.TrimSpace(wire.GetResourceName()) == "" || len(wire.GetResourceName()) > 255 {
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}

	// Guard 5: Chuẩn phân tán W3C Traceparent
	if !validTraceparent {
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}

	// 3. Tính mã băm SHA-256 của payload để phục vụ dedupe và đối soát toàn vẹn trong Inbox
	hash := sha256.Sum256(payload)
	event := &entity.ResourceOwnershipEvent{
		EventID:        eventID,
		ResourceType:   wire.GetResourceType(),
		ResourceID:     resourceID,
		ResourceName:   wire.GetResourceName(),
		OwnerID:        ownerID,
		OwnerType:      wire.GetOwnerType(),
		ZoneID:         zoneID,
		SourceVersion:  wire.GetSourceVersion(),
		EffectiveAt:    effectiveAt,
		EventType:      eventType,
		PayloadHashHex: hex.EncodeToString(hash[:]),
	}

	// 4. Chuyển giao xuống Service Layer để thực thi giao dịch ghi vào PostgreSQL Billing DB
	applyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := s.ownership.ProcessResourceOwnershipEvent(applyCtx, event)
	cancel()
	if err != nil {
		// Nếu là lỗi xung đột toàn vẹn dữ liệu không thể phục hồi -> đưa vào DLQ
		if errors.Is(err, billingTaxonomy.ErrResourceOwnershipIntegrity) {
			s.deadLetter(ctx, message, "integrity_conflict")
			return
		}
		// Lỗi kết nối DB tạm thời KHÔNG PHẢI là tin nhắn độc (poison).
		// Giữ tin nhắn ở trạng thái Pending trong Redis; cơ chế XAutoClaim sẽ retry sau khi DB hồi phục.
		logger.SysError(
			"billing.ownership.redis.apply",
			fmt.Sprintf("event_id=%s: %v", eventID, err),
		)
		return
	}

	// 5. Giao dịch thành công: Thực hiện pipeline XACK và XDEL để xóa vĩnh viễn tin nhắn khỏi Redis Stream
	if _, err := s.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAck(ctx, resourceOwnershipStream, resourceOwnershipGroup, message.ID)
		pipe.XDel(ctx, resourceOwnershipStream, message.ID)
		return nil
	}); err != nil {
		// Nếu lệnh ACK thất bại do mạng, việc gửi lại (redelivery) vẫn an toàn (Idempotent)
		// vì bảng `billing.ownership_event_inbox` đã lưu event_id và payload_hash trong cùng 1 transaction.
		logger.SysError("billing.ownership.redis.ack", err.Error())
	}
}

// deadLetter cách ly tin nhắn lỗi sang Dead-Letter Queue (DLQ) và ACK khỏi Stream chính.
func (s *ResourceOwnershipConsumer) deadLetter(
	ctx context.Context,
	message goredis.XMessage,
	reason string,
) {
	var payload []byte
	switch v := message.Values["payload"].(type) {
	case []byte:
		payload = v
	case string:
		payload = []byte(v)
	}
	payloadHash := sha256.Sum256(payload)

	var eventIDText string
	switch v := message.Values["event_id"].(type) {
	case string:
		eventIDText = v
	case []byte:
		eventIDText = string(v)
	}

	var eventTypeText string
	switch v := message.Values["event_type"].(type) {
	case string:
		eventTypeText = v
	case []byte:
		eventTypeText = string(v)
	}

	if _, err := s.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAdd(ctx, &goredis.XAddArgs{
			Stream: resourceOwnershipDLQ,
			MaxLen: resourceOwnershipDLQSize,
			Approx: true,
			Values: map[string]any{
				"source_stream_id": message.ID,
				"reason":           reason,
				"event_id":         eventIDText,
				"event_type":       eventTypeText,
				"payload_len":      len(payload),
				"payload_sha256":   hex.EncodeToString(payloadHash[:]),
			},
		})
		pipe.XAck(ctx, resourceOwnershipStream, resourceOwnershipGroup, message.ID)
		pipe.XDel(ctx, resourceOwnershipStream, message.ID)
		return nil
	}); err != nil {
		logger.SysError("billing.ownership.redis.dlq", err.Error())
		return
	}
	logger.SysWarn("billing.ownership.redis.dlq", "ownership event moved to DLQ: "+reason)
}

// Stop dừng hoạt động Consumer một cách an toàn (Graceful Shutdown).
func (s *ResourceOwnershipConsumer) Stop() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.cancel == nil {
			return
		}
		s.cancel()
		select {
		case <-s.done:
		case <-time.After(6 * time.Second):
			logger.SysWarn("billing.ownership.redis.stop", "timed out waiting for ownership consumer")
		}
	})
}
