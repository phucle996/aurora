/*
============================================================================
MAP: BILLING NATS TRANSPORT - LIFECYCLE EVENT HANDLER
============================================================================
CONTRACT:
1. Đặt tại transport/nats/handler theo đúng phân lớp kiến trúc.
2. Tiêu thụ NATS JetStream message từ Stream CONTROLPLANE_DOMAIN_EVENTS (Consumer: cost-ownership-v1).
3. Extract headers/payload, parse Protobuf/UUID và cập nhật Billing ownership projection.
============================================================================
*/

package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"

	"cost-manager/api/internal/domain/entity"
	ownershipv1 "cost-manager/api/internal/genproto/billing/ownership/v1"
	"cost-manager/api/internal/service"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: ResourceOwnershipSubscriber nhận ownership event và chuyển giao cho Service Layer.
type ResourceOwnershipSubscriber struct {
	nc           *nats.Conn
	js           jetstream.JetStream
	consumer     jetstream.Consumer
	ownershipSvc service.ResourceOwnershipService
	stopChan     chan struct{}
}

// [COMMENT]: NewResourceOwnershipSubscriber khởi tạo durable ownership consumer.
func NewResourceOwnershipSubscriber(nc *nats.Conn, ownershipSvc service.ResourceOwnershipService) (*ResourceOwnershipSubscriber, error) {
	if nc == nil {
		return nil, errors.New("nats connection cannot be nil")
	}

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("create jetstream context failed: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      "CONTROLPLANE_DOMAIN_EVENTS",
		Subjects:  []string{"billing.ownership.resource.changed.v1"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    72 * time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("create or update stream failed: %w", err)
	}

	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       "cost-ownership-v1",
		FilterSubject: "billing.ownership.resource.changed.v1",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    10,
	})
	if err != nil {
		return nil, fmt.Errorf("create or update consumer failed: %w", err)
	}

	return &ResourceOwnershipSubscriber{
		nc:           nc,
		js:           js,
		consumer:     consumer,
		ownershipSvc: ownershipSvc,
		stopChan:     make(chan struct{}),
	}, nil
}

// [COMMENT]: Start bắt đầu tiêu thụ bất đồng bộ các message từ NATS JetStream Consumer và xử lý trực tiếp trong callback inline.
func (sub *ResourceOwnershipSubscriber) Start(ctx context.Context) error {
	log.Println("[ResourceOwnership] Khởi chạy JetStream consumer cost-ownership-v1...")

	cons, err := sub.consumer.Consume(func(msg jetstream.Msg) {
		payload := msg.Data()
		hash := sha256.Sum256(payload)
		payloadHashHex := hex.EncodeToString(hash[:])

		eventIDStr := msg.Headers().Get("Nats-Msg-Id")
		if eventIDStr == "" {
			log.Printf("[ResourceOwnership] Thiếu Nats-Msg-Id header. NAK message.")
			_ = msg.Nak()
			return
		}

		eventID, err := uuid.Parse(eventIDStr)
		if err != nil || eventID == uuid.Nil {
			log.Printf("[ResourceOwnership] UUID event_id không hợp lệ hoặc Nil (%s). NAK message.", eventIDStr)
			_ = msg.Nak()
			return
		}

		// [COMMENT]: Payload Protobuf là dữ liệu nghiệp vụ; Nats-Msg-Id chỉ là khóa
		// dedup. Không được ACK một entity rỗng chỉ vì header hợp lệ.
		wireEvent := &ownershipv1.ResourceOwnershipChangedV1{}
		if err := proto.Unmarshal(payload, wireEvent); err != nil {
			log.Printf("[ResourceOwnership] Payload protobuf không hợp lệ: %v. NAK message.", err)
			_ = msg.Nak()
			return
		}

		payloadEventID, err := uuid.FromBytes(wireEvent.GetEventId())
		if err != nil || payloadEventID != eventID {
			log.Printf("[ResourceOwnership] event_id payload/header không khớp. NAK message.")
			_ = msg.Nak()
			return
		}
		resourceID, resourceErr := uuid.FromBytes(wireEvent.GetResourceId())
		ownerID, ownerErr := uuid.FromBytes(wireEvent.GetOwnerId())
		zoneID, zoneErr := uuid.FromBytes(wireEvent.GetZoneId())
		sourceJobID, sourceJobErr := uuid.FromBytes(wireEvent.GetSourceJobId())
		effectiveAt, timeErr := time.Parse(time.RFC3339Nano, wireEvent.GetEffectiveAt())
		eventType := entity.ResourceOwnershipEventType(wireEvent.GetEventType())
		validEventType := eventType == entity.ResourceOwnershipEventCreated || eventType == entity.ResourceOwnershipEventDeleted
		validOwnerType := wireEvent.GetOwnerType() == "PERSONAL" || wireEvent.GetOwnerType() == "TENANT"
		if resourceErr != nil || ownerErr != nil || zoneErr != nil || sourceJobErr != nil || timeErr != nil ||
			resourceID == uuid.Nil || ownerID == uuid.Nil || zoneID == uuid.Nil || sourceJobID == uuid.Nil ||
			wireEvent.GetSchemaVersion() != 1 || wireEvent.GetSourceVersion() <= 0 ||
			wireEvent.GetResourceType() == "" || wireEvent.GetResourceName() == "" ||
			!validEventType || !validOwnerType {
			log.Printf("[ResourceOwnership] Resource ownership contract không hợp lệ. NAK message.")
			_ = msg.Nak()
			return
		}

		eventEntity := &entity.ResourceOwnershipEvent{
			EventID:        eventID,
			ResourceType:   wireEvent.GetResourceType(),
			ResourceID:     resourceID,
			ResourceName:   wireEvent.GetResourceName(),
			OwnerID:        ownerID,
			OwnerType:      wireEvent.GetOwnerType(),
			ZoneID:         zoneID,
			SourceVersion:  wireEvent.GetSourceVersion(),
			EffectiveAt:    effectiveAt,
			EventType:      eventType,
			PayloadHashHex: payloadHashHex,
		}

		msgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := sub.ownershipSvc.ProcessResourceOwnershipEvent(msgCtx, eventEntity); err != nil {
			log.Printf("[ResourceOwnership] Lỗi xử lý service layer: %v. NAK message.", err)
			_ = msg.Nak()
			return
		}

		_ = msg.Ack()
	})

	if err != nil {
		return fmt.Errorf("start consume failed: %w", err)
	}

	go func() {
		<-sub.stopChan
		cons.Stop()
		log.Println("[ResourceOwnership] Đã dừng consumer loop.")
	}()

	return nil
}

// [COMMENT]: Stop phát tín hiệu dừng tiêu thụ message.
func (sub *ResourceOwnershipSubscriber) Stop() {
	close(sub.stopChan)
}
