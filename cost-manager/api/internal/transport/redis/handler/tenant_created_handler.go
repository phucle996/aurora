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

	billingSvcInterface "cost-manager/api/internal/domain/service"
	iamlifecyclev1 "cost-manager/api/internal/genproto/iam/lifecycle/v1"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	tenantCreatedEventType    = "hierarchy.tenant.created.v1"
	tenantCreatedStream       = "hierarchy:tenant:created:v1"
	tenantCreatedGroup        = "cost-tenant-created-wallet-adapter-v1"
	tenantCreatedDLQ          = "billing:wallet:tenant:provision-dlq"
	tenantWalletReclaimIdle   = 30 * time.Second
	tenantWalletMaxDeliveries = 25
)

// TenantCreatedConsumer has a distinct stream/group/DLQ so a poison
// tenant event cannot create backpressure on personal account verification.
type TenantCreatedConsumer struct {
	sharedRedis *goredis.Client
	service     billingSvcInterface.TenantAccountService
	consumer    string

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func NewTenantCreatedConsumer(
	sharedRedis *goredis.Client,
	service billingSvcInterface.TenantAccountService,
) (*TenantCreatedConsumer, error) {
	if sharedRedis == nil || service == nil {
		return nil, errors.New("tenant-created wallet adapter requires Shared Redis and TenantAccountService")
	}
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "cost-manager"
	}
	return &TenantCreatedConsumer{
		sharedRedis: sharedRedis,
		service:     service,
		consumer:    hostname + "-" + uuid.NewString(),
		done:        make(chan struct{}),
	}, nil
}

func (s *TenantCreatedConsumer) Start() error {
	if s == nil {
		return errors.New("tenant wallet provision consumer is nil")
	}
	if s.cancel != nil {
		return errors.New("tenant wallet provision consumer already started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.sharedRedis.XGroupCreateMkStream(
		ctx,
		tenantCreatedStream,
		tenantCreatedGroup,
		"0",
	).Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		return fmt.Errorf("tenant wallet provision: create consumer group: %w", err)
	}
	s.cancel = cancel
	go s.run(ctx)
	return nil
}

func (s *TenantCreatedConsumer) run(ctx context.Context) {
	defer close(s.done)
	for {
		if ctx.Err() != nil {
			return
		}
		claimed, _, err := s.sharedRedis.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
			Stream:   tenantCreatedStream,
			Group:    tenantCreatedGroup,
			Consumer: s.consumer,
			MinIdle:  tenantWalletReclaimIdle,
			Start:    "0-0",
			Count:    32,
		}).Result()
		if err != nil && !errors.Is(err, goredis.Nil) {
			if !waitTenantWalletRetry(ctx, "billing.wallet.tenant.redis.reclaim", err) {
				return
			}
			continue
		}
		for _, message := range claimed {
			s.process(ctx, message)
		}
		if len(claimed) > 0 {
			continue
		}

		streams, err := s.sharedRedis.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group:    tenantCreatedGroup,
			Consumer: s.consumer,
			Streams:  []string{tenantCreatedStream, ">"},
			Count:    32,
			Block:    5 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				continue
			}
			if !waitTenantWalletRetry(ctx, "billing.wallet.tenant.redis.read", err) {
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

func waitTenantWalletRetry(ctx context.Context, operation string, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	logger.SysError(operation, err.Error())
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *TenantCreatedConsumer) process(ctx context.Context, message goredis.XMessage) {
	eventIDText := walletStreamString(message.Values["event_id"])
	eventType := walletStreamString(message.Values["event_type"])
	payload := walletStreamBytes(message.Values["payload"])
	if len(payload) == 0 || len(payload) > 64*1024 {
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}

	wire := &iamlifecyclev1.TenantCreatedV1{}
	eventID, eventErr := uuid.Parse(eventIDText)
	protoErr := proto.Unmarshal(payload, wire)
	wireEventID, wireEventErr := uuid.FromBytes(wire.GetEventId())
	tenantID, tenantErr := uuid.FromBytes(wire.GetTenantId())
	actorID, actorErr := uuid.FromBytes(wire.GetActorUserId())
	_, occurredErr := time.Parse(time.RFC3339Nano, wire.GetOccurredAt())
	if eventErr != nil || protoErr != nil || wireEventErr != nil ||
		tenantErr != nil || actorErr != nil ||
		eventID == uuid.Nil || wireEventID != eventID ||
		tenantID == uuid.Nil || actorID == uuid.Nil ||
		eventType != tenantCreatedEventType ||
		wire.GetSchemaVersion() != 1 || occurredErr != nil {
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}

	hash := sha256.Sum256(payload)
	applyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := s.service.ProvisionTenantWallet(
		applyCtx,
		eventID,
		tenantID,
		actorID,
		hex.EncodeToString(hash[:]),
	)
	cancel()
	if err != nil {
		attemptKey := "billing:wallet:tenant:delivery-attempts:" + message.ID
		attempts, countErr := s.sharedRedis.Incr(ctx, attemptKey).Result()
		if countErr == nil {
			_ = s.sharedRedis.Expire(ctx, attemptKey, 30*24*time.Hour).Err()
		}
		logger.SysError("billing.wallet.tenant.redis.apply", fmt.Sprintf("event_id=%s: %v", eventID, err))
		if countErr == nil && attempts >= tenantWalletMaxDeliveries {
			s.deadLetter(ctx, message, "apply_retries_exhausted")
		}
		return
	}

	if _, err := s.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAck(ctx, tenantCreatedStream, tenantCreatedGroup, message.ID)
		pipe.XDel(ctx, tenantCreatedStream, message.ID)
		pipe.Del(ctx, "billing:wallet:tenant:delivery-attempts:"+message.ID)
		return nil
	}); err != nil {
		logger.SysError("billing.wallet.tenant.redis.ack", err.Error())
	}
}

func walletStreamString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

func walletStreamBytes(value any) []byte {
	switch typed := value.(type) {
	case string:
		return []byte(typed)
	case []byte:
		return typed
	default:
		return nil
	}
}

func (s *TenantCreatedConsumer) deadLetter(
	ctx context.Context,
	message goredis.XMessage,
	reason string,
) {
	eventID := message.Values["event_id"]
	if eventID == nil {
		eventID = ""
	}
	eventType := message.Values["event_type"]
	if eventType == nil {
		eventType = ""
	}
	payload := message.Values["payload"]
	if payload == nil {
		payload = []byte{}
	}
	if _, err := s.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAdd(ctx, &goredis.XAddArgs{
			Stream: tenantCreatedDLQ,
			Values: map[string]any{
				"source_stream_id": message.ID,
				"reason":           reason,
				"event_id":         eventID,
				"event_type":       eventType,
				"payload":          payload,
			},
		})
		pipe.XAck(ctx, tenantCreatedStream, tenantCreatedGroup, message.ID)
		pipe.XDel(ctx, tenantCreatedStream, message.ID)
		pipe.Del(ctx, "billing:wallet:tenant:delivery-attempts:"+message.ID)
		return nil
	}); err != nil {
		logger.SysError("billing.wallet.tenant.redis.dlq", err.Error())
		return
	}
	logger.SysWarn("billing.wallet.tenant.redis.dlq", "tenant wallet provision event moved to DLQ: "+reason)
}

func (s *TenantCreatedConsumer) Stop() {
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
			logger.SysWarn("billing.wallet.tenant.redis.stop", "timed out waiting for tenant wallet provision consumer")
		}
	})
}
