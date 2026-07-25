package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"cost-manager/api/internal/domain/entity"
	ownershipv1 "cost-manager/api/internal/genproto/billing/ownership/v1"
	"cost-manager/api/internal/service"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	resourceOwnershipStream  = "stream:{billing}:resource_ownership"
	resourceOwnershipGroup   = "cost-resource-ownership-v1"
	resourceOwnershipDLQ     = "stream:{billing}:resource_ownership:dlq"
	resourceOwnershipReclaim = 30 * time.Second
	resourceOwnershipBatch   = int64(32)
	resourceOwnershipMaxSize = 64 * 1024
	resourceOwnershipDLQSize = int64(10_000)
)

// ResourceOwnershipConsumer is the Central-internal ownership transport.
// Billing PostgreSQL inbox remains the idempotency and apply boundary.
type ResourceOwnershipConsumer struct {
	sharedRedis *goredis.Client
	ownership   service.ResourceOwnershipService
	consumer    string

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func NewResourceOwnershipConsumer(
	sharedRedis *goredis.Client,
	ownership service.ResourceOwnershipService,
) (*ResourceOwnershipConsumer, error) {
	if sharedRedis == nil || ownership == nil {
		return nil, errors.New("resource ownership consumer requires Shared Redis and ownership service")
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "cost-manager"
	}
	return &ResourceOwnershipConsumer{
		sharedRedis: sharedRedis,
		ownership:   ownership,
		consumer:    host + "-" + uuid.NewString(),
		done:        make(chan struct{}),
	}, nil
}

func (s *ResourceOwnershipConsumer) Start() error {
	if s == nil {
		return errors.New("resource ownership consumer is nil")
	}
	if s.cancel != nil {
		return errors.New("resource ownership consumer already started")
	}
	ctx, cancel := context.WithCancel(context.Background())
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

func (s *ResourceOwnershipConsumer) run(ctx context.Context) {
	defer close(s.done)
	claimCursor := "0-0"
	for ctx.Err() == nil {
		// [COMMENT]: Apply timeout is 10 seconds; reclaim only after 30 seconds so
		// a healthy pod is not raced by another replica while committing Billing DB.
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
			if !waitContext(ctx, time.Second) {
				return
			}
			continue
		}
		// [COMMENT]: Preserve Redis' scan cursor so a poison entry near the head
		// cannot starve older pending entries beyond the first claim batch.
		claimCursor = nextCursor
		for _, message := range claimed {
			s.process(ctx, message)
		}
		if len(claimed) > 0 {
			continue
		}
		if claimCursor != "0-0" {
			continue
		}

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
			if !waitContext(ctx, time.Second) {
				return
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

func (s *ResourceOwnershipConsumer) process(ctx context.Context, message goredis.XMessage) {
	eventIDText := redisString(message.Values["event_id"])
	eventTypeText := redisString(message.Values["event_type"])
	payload := redisBytes(message.Values["payload"])
	if len(payload) == 0 || len(payload) > resourceOwnershipMaxSize {
		s.deadLetter(ctx, message, "invalid_payload_size")
		return
	}

	wire := &ownershipv1.ResourceOwnershipChangedV1{}
	eventID, eventErr := uuid.Parse(eventIDText)
	protoErr := proto.Unmarshal(payload, wire)
	wireEventID, wireEventErr := uuid.FromBytes(wire.GetEventId())
	resourceID, resourceErr := uuid.FromBytes(wire.GetResourceId())
	ownerID, ownerErr := uuid.FromBytes(wire.GetOwnerId())
	zoneID, zoneErr := uuid.FromBytes(wire.GetZoneId())
	sourceJobID, sourceJobErr := uuid.FromBytes(wire.GetSourceJobId())
	effectiveAt, timeErr := time.Parse(time.RFC3339Nano, wire.GetEffectiveAt())
	eventType := entity.ResourceOwnershipEventType(wire.GetEventType())
	validEventType := eventType == entity.ResourceOwnershipEventCreated ||
		eventType == entity.ResourceOwnershipEventDeleted
	validOwnerType := wire.GetOwnerType() == "PERSONAL" || wire.GetOwnerType() == "TENANT"
	if eventErr != nil || protoErr != nil || wireEventErr != nil ||
		resourceErr != nil || ownerErr != nil || zoneErr != nil || sourceJobErr != nil ||
		timeErr != nil || eventID == uuid.Nil || wireEventID != eventID ||
		eventTypeText != wire.GetEventType() || resourceID == uuid.Nil ||
		ownerID == uuid.Nil || zoneID == uuid.Nil || sourceJobID == uuid.Nil ||
		wire.GetSchemaVersion() != 1 || wire.GetSourceVersion() <= 0 ||
		wire.GetResourceType() != "STORAGE_BUCKET" ||
		strings.TrimSpace(wire.GetResourceName()) == "" ||
		len(wire.GetResourceName()) > 255 || !validTraceparent(wire.GetTraceparent()) ||
		!validEventType || !validOwnerType {
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}

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
	applyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := s.ownership.ProcessResourceOwnershipEvent(applyCtx, event)
	cancel()
	if err != nil {
		if errors.Is(err, entity.ErrResourceOwnershipIntegrity) {
			s.deadLetter(ctx, message, "integrity_conflict")
			return
		}
		// [COMMENT]: Billing/DB failures are not poison. Keep the message pending
		// indefinitely and alert; XAUTOCLAIM provides failover without data loss.
		logger.SysError(
			"billing.ownership.redis.apply",
			fmt.Sprintf("event_id=%s: %v", eventID, err),
		)
		return
	}

	if _, err := s.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAck(ctx, resourceOwnershipStream, resourceOwnershipGroup, message.ID)
		pipe.XDel(ctx, resourceOwnershipStream, message.ID)
		return nil
	}); err != nil {
		// Redelivery is safe because billing.ownership_event_inbox stores
		// event_id plus payload hash in the same Billing transaction.
		logger.SysError("billing.ownership.redis.ack", err.Error())
	}
}

func (s *ResourceOwnershipConsumer) deadLetter(
	ctx context.Context,
	message goredis.XMessage,
	reason string,
) {
	payload := redisBytes(message.Values["payload"])
	payloadHash := sha256.Sum256(payload)
	// All keys share the {billing} hash tag, so DLQ + ACK + delete remains one
	// Redis Cluster transaction. Never copy arbitrary rejected bytes into a
	// second store; the bounded fingerprint is sufficient for diagnosis.
	if _, err := s.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAdd(ctx, &goredis.XAddArgs{
			Stream: resourceOwnershipDLQ,
			MaxLen: resourceOwnershipDLQSize,
			Approx: true,
			Values: map[string]any{
				"source_stream_id": message.ID,
				"reason":           reason,
				"event_id":         redisString(message.Values["event_id"]),
				"event_type":       redisString(message.Values["event_type"]),
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

func redisString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

func redisBytes(value any) []byte {
	switch typed := value.(type) {
	case string:
		return []byte(typed)
	case []byte:
		return typed
	default:
		return nil
	}
}

func validTraceparent(value string) bool {
	if value == "" {
		return true
	}
	parts := strings.Split(value, "-")
	if len(parts) != 4 || len(parts[0]) != 2 || parts[0] == "ff" ||
		len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return false
	}
	version, versionErr := hex.DecodeString(parts[0])
	traceID, traceErr := hex.DecodeString(parts[1])
	spanID, spanErr := hex.DecodeString(parts[2])
	flags, flagsErr := hex.DecodeString(parts[3])
	return versionErr == nil && traceErr == nil && spanErr == nil && flagsErr == nil &&
		len(version) == 1 && len(flags) == 1 && !allZero(traceID) && !allZero(spanID)
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

func waitContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
