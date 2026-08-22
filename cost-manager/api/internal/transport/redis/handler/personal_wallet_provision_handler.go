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
	billingSvcInterface "cost-manager/api/internal/domain/service"
	iamlifecyclev1 "cost-manager/api/internal/genproto/iam/lifecycle/v1"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	personalWalletProvisionEventType = "billing.personal_wallet.provision.requested.v1"
	personalWalletProvisionStream    = "billing:personal-wallet:provision:requested:v1"
	personalWalletProvisionGroup     = "cost-personal-wallet-provision-v1"
	personalWalletProvisionDLQ       = "billing:wallet:personal:provision-dlq"
	personalWalletReclaimIdle        = 30 * time.Second
	personalWalletMaxDeliveries      = 25
)

// PersonalWalletProvisionConsumer applies IAM's billing command to Cost's wallet projection.
// PostgreSQL inbox + wallet mutation remains the idempotency and apply boundary.
type PersonalWalletProvisionConsumer struct {
	sharedRedis *goredis.Client
	service     billingSvcInterface.PersonalAccountService
	consumer    string

	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func NewPersonalWalletProvisionConsumer(
	sharedRedis *goredis.Client,
	service billingSvcInterface.PersonalAccountService,
) *PersonalWalletProvisionConsumer {
	return &PersonalWalletProvisionConsumer{
		sharedRedis: sharedRedis,
		service:     service,
		// [COMMENT]: Consumer identity riêng theo process cho phép XAUTOCLAIM tiếp quản pending
		// của pod đã chết mà không để nhiều pod cùng xử lý một delivery đang còn lease.
		consumer: config.GetNodeHostname() + "-" + uuid.NewString(),
		done:     make(chan struct{}),
	}
}

func (s *PersonalWalletProvisionConsumer) Start() error {
	if s.cancel != nil {
		return errors.New("personal wallet provision consumer already started")
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.sharedRedis.XGroupCreateMkStream(
		ctx,
		personalWalletProvisionStream,
		personalWalletProvisionGroup,
		"0",
	).Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		return fmt.Errorf("personal wallet provision: create consumer group: %w", err)
	}
	s.cancel = cancel
	go s.run(ctx)
	return nil
}

func (s *PersonalWalletProvisionConsumer) run(ctx context.Context) {
	defer close(s.done)

	for {
		if ctx.Err() != nil {
			return
		}

		// [COMMENT]: Claim chỉ xảy ra sau 30 giây idle. Khoảng này lớn hơn timeout apply
		// 10 giây nên pod mới không cướp message trong khi pod cũ vẫn đang commit Billing DB.
		claimed, _, err := s.sharedRedis.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
			Stream:   personalWalletProvisionStream,
			Group:    personalWalletProvisionGroup,
			Consumer: s.consumer,
			MinIdle:  personalWalletReclaimIdle,
			Start:    "0-0",
			Count:    32,
		}).Result()
		if err != nil && !errors.Is(err, goredis.Nil) {
			if ctx.Err() != nil {
				return
			}
			logger.SysError("billing.wallet.redis.reclaim", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
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
			Group:    personalWalletProvisionGroup,
			Consumer: s.consumer,
			Streams:  []string{personalWalletProvisionStream, ">"},
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
			logger.SysError("billing.wallet.redis.read", err.Error())
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

func (s *PersonalWalletProvisionConsumer) process(ctx context.Context, message goredis.XMessage) {
	var eventIDText string
	switch value := message.Values["event_id"].(type) {
	case string:
		eventIDText = value
	case []byte:
		eventIDText = string(value)
	}
	var eventType string
	switch value := message.Values["event_type"].(type) {
	case string:
		eventType = value
	case []byte:
		eventType = string(value)
	}
	var payload []byte
	switch value := message.Values["payload"].(type) {
	case string:
		payload = []byte(value)
	case []byte:
		payload = value
	}

	if len(payload) == 0 || len(payload) > 64*1024 {
		// [COMMENT]: Giới hạn trước protobuf decode để writer bị compromise không thể
		// ép Cost Manager allocate/parse payload vô hạn trên consumer loop.
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}
	wire := &iamlifecyclev1.PersonalWalletProvisionRequestedV1{}
	eventID, eventErr := uuid.Parse(eventIDText)
	protoErr := proto.Unmarshal(payload, wire)
	wireEventID, wireEventErr := uuid.FromBytes(wire.GetEventId())
	ownerID, ownerErr := uuid.FromBytes(wire.GetOwnerId())
	_, occurredErr := time.Parse(time.RFC3339Nano, wire.GetOccurredAt())
	if eventErr != nil || protoErr != nil || wireEventErr != nil || ownerErr != nil ||
		eventID == uuid.Nil || wireEventID != eventID || ownerID == uuid.Nil ||
		eventType != personalWalletProvisionEventType || wire.GetOwnerType() != "PERSONAL" ||
		wire.GetCurrency() != "USD" || wire.GetSchemaVersion() != 1 || occurredErr != nil {
		// [COMMENT]: Poison contract không thể tự hồi phục. DLQ + ACK + XDEL nằm cùng
		// Redis transaction; nếu transaction lỗi, original vẫn pending để không mất bằng chứng.
		s.deadLetter(ctx, message, "invalid_contract")
		return
	}

	hash := sha256.Sum256(payload)
	applyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err := s.service.ProvisionPersonalWallet(
		applyCtx,
		eventID,
		ownerID,
		hex.EncodeToString(hash[:]),
	)
	cancel()
	if err != nil {
		attemptKey := "billing:wallet:personal:delivery-attempts:" + message.ID
		attempts, countErr := s.sharedRedis.Incr(ctx, attemptKey).Result()
		if countErr == nil {
			_ = s.sharedRedis.Expire(ctx, attemptKey, 30*24*time.Hour).Err()
		}
		logger.SysError("billing.wallet.redis.apply", fmt.Sprintf("event_id=%s: %v", eventID, err))
		if countErr == nil && attempts >= personalWalletMaxDeliveries {
			s.deadLetter(ctx, message, "apply_retries_exhausted")
		}
		// [COMMENT]: Không ACK khi Billing transaction lỗi. Message chỉ được claim lại
		// sau MinIdle, tránh vòng retry nóng làm nghẽn PostgreSQL khi dependency outage.
		return
	}

	// [COMMENT]: Service chỉ trả nil sau inbox + wallet commit. ACK và XDEL atomic ở Redis;
	// nếu response MULTI/EXEC bị mất, redelivery vẫn an toàn nhờ event inbox hash.
	if _, err := s.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAck(ctx, personalWalletProvisionStream, personalWalletProvisionGroup, message.ID)
		pipe.XDel(ctx, personalWalletProvisionStream, message.ID)
		pipe.Del(ctx, "billing:wallet:personal:delivery-attempts:"+message.ID)
		return nil
	}); err != nil {
		logger.SysError("billing.wallet.redis.ack", err.Error())
	}
}

func (s *PersonalWalletProvisionConsumer) deadLetter(
	ctx context.Context,
	message goredis.XMessage,
	reason string,
) {
	// [COMMENT]: DLQ không chứa error string tự do để tránh leak dữ liệu và cardinality vô hạn.
	// Original payload được giữ nguyên để operator có thể audit/replay cùng event_id.
	if _, err := s.sharedRedis.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAdd(ctx, &goredis.XAddArgs{
			Stream: personalWalletProvisionDLQ,
			Values: map[string]any{
				"source_stream_id": message.ID,
				"reason":           reason,
				"event_id":         message.Values["event_id"],
				"event_type":       message.Values["event_type"],
				"payload":          message.Values["payload"],
			},
		})
		pipe.XAck(ctx, personalWalletProvisionStream, personalWalletProvisionGroup, message.ID)
		pipe.XDel(ctx, personalWalletProvisionStream, message.ID)
		pipe.Del(ctx, "billing:wallet:personal:delivery-attempts:"+message.ID)
		return nil
	}); err != nil {
		logger.SysError("billing.wallet.redis.dlq", err.Error())
		return
	}
	logger.SysWarn("billing.wallet.redis.dlq", "wallet provision event moved to DLQ: "+reason)
}

func (s *PersonalWalletProvisionConsumer) Stop() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		select {
		case <-s.done:
		case <-time.After(6 * time.Second):
			logger.SysWarn("billing.wallet.redis.stop", "timed out waiting for wallet provision consumer")
		}
	})
}
