/*
============================================================================
MAP: BILLING NATS TRANSPORT - LIFECYCLE EVENT HANDLER
============================================================================
CONTRACT:
1. Đặt tại transport/nats/handler theo đúng phân lớp kiến trúc.
2. Tiêu thụ NATS JetStream message từ Stream CONTROLPLANE_DOMAIN_EVENTS (Consumer: cost-ownership-v1).
3. Extract headers/payload, parse Protobuf/UUID và gọi sang LifecycleService.
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
	"cost-manager/api/internal/service"
	pb "cost-manager/api/internal/transport/proto"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: LifecycleNatsSubscriber điều phối việc nhận NATS JetStream message và chuyển giao cho Service Layer.
type LifecycleNatsSubscriber struct {
	nc           *nats.Conn
	js           jetstream.JetStream
	consumer     jetstream.Consumer
	lifecycleSvc service.LifecycleService
	stopChan     chan struct{}
}

// [COMMENT]: NewLifecycleNatsSubscriber khởi tạo JetStream Stream và Durable Consumer cho Lifecycle Events.
func NewLifecycleNatsSubscriber(nc *nats.Conn, lifecycleSvc service.LifecycleService) (*LifecycleNatsSubscriber, error) {
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
		Subjects:  []string{"controlplane.storage.resource.lifecycle.v1"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    72 * time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("create or update stream failed: %w", err)
	}

	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       "cost-ownership-v1",
		FilterSubject: "controlplane.storage.resource.lifecycle.v1",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    10,
	})
	if err != nil {
		return nil, fmt.Errorf("create or update consumer failed: %w", err)
	}

	return &LifecycleNatsSubscriber{
		nc:           nc,
		js:           js,
		consumer:     consumer,
		lifecycleSvc: lifecycleSvc,
		stopChan:     make(chan struct{}),
	}, nil
}

// [COMMENT]: Start bắt đầu tiêu thụ bất đồng bộ các message từ NATS JetStream Consumer và xử lý trực tiếp trong callback inline.
func (sub *LifecycleNatsSubscriber) Start(ctx context.Context) error {
	log.Println("[NATS LifecycleHandler] Khởi chạy JetStream consumer cost-ownership-v1...")

	cons, err := sub.consumer.Consume(func(msg jetstream.Msg) {
		payload := msg.Data()
		hash := sha256.Sum256(payload)
		payloadHashHex := hex.EncodeToString(hash[:])

		eventIDStr := msg.Headers().Get("Nats-Msg-Id")
		if eventIDStr == "" {
			log.Printf("[NATS LifecycleHandler] Thiếu Nats-Msg-Id header. NAK message.")
			_ = msg.Nak()
			return
		}

		eventID, err := uuid.Parse(eventIDStr)
		if err != nil || eventID == uuid.Nil {
			log.Printf("[NATS LifecycleHandler] UUID event_id không hợp lệ hoặc Nil (%s). NAK message.", eventIDStr)
			_ = msg.Nak()
			return
		}

		// [COMMENT]: Payload Protobuf là dữ liệu nghiệp vụ; Nats-Msg-Id chỉ là khóa
		// dedup. Không được ACK một entity rỗng chỉ vì header hợp lệ.
		wireEvent := &pb.ResourceLifecycleEventV1{}
		if err := proto.Unmarshal(payload, wireEvent); err != nil {
			log.Printf("[NATS LifecycleHandler] Payload protobuf không hợp lệ: %v. NAK message.", err)
			_ = msg.Nak()
			return
		}

		payloadEventID, err := uuid.FromBytes(wireEvent.GetEventId())
		if err != nil || payloadEventID != eventID {
			log.Printf("[NATS LifecycleHandler] event_id payload/header không khớp. NAK message.")
			_ = msg.Nak()
			return
		}
		resourceID, resourceErr := uuid.FromBytes(wireEvent.GetResourceId())
		ownerID, ownerErr := uuid.FromBytes(wireEvent.GetOwnerId())
		zoneID, zoneErr := uuid.FromBytes(wireEvent.GetZoneId())
		sourceJobID, sourceJobErr := uuid.FromBytes(wireEvent.GetSourceJobId())
		effectiveAt, timeErr := time.Parse(time.RFC3339Nano, wireEvent.GetEffectiveAt())
		eventType := entity.ResourceLifecycleEventType(wireEvent.GetEventType())
		validEventType := eventType == entity.ResourceLifecycleEventCreated || eventType == entity.ResourceLifecycleEventDeleted
		validOwnerType := wireEvent.GetOwnerType() == "PERSONAL" || wireEvent.GetOwnerType() == "TENANT"
		if resourceErr != nil || ownerErr != nil || zoneErr != nil || sourceJobErr != nil || timeErr != nil ||
			resourceID == uuid.Nil || ownerID == uuid.Nil || zoneID == uuid.Nil || sourceJobID == uuid.Nil ||
			wireEvent.GetSchemaVersion() != 1 || wireEvent.GetSourceVersion() <= 0 ||
			wireEvent.GetResourceType() == "" || wireEvent.GetResourceName() == "" ||
			!validEventType || !validOwnerType {
			log.Printf("[NATS LifecycleHandler] Lifecycle contract không hợp lệ. NAK message.")
			_ = msg.Nak()
			return
		}

		eventEntity := &entity.LifecycleEvent{
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

		if err := sub.lifecycleSvc.ProcessLifecycleEvent(msgCtx, eventEntity); err != nil {
			log.Printf("[NATS LifecycleHandler] Lỗi xử lý service layer: %v. NAK message.", err)
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
		log.Println("[NATS LifecycleHandler] Đã dừng consumer loop.")
	}()

	return nil
}

// [COMMENT]: Stop phát tín hiệu dừng tiêu thụ message.
func (sub *LifecycleNatsSubscriber) Stop() {
	close(sub.stopChan)
}
