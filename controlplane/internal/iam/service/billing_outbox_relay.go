package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const personalWalletProvisionEventType = "billing.wallet.personal.provision.requested.v1"
const personalWalletProvisionStream = "billing:wallet:personal:provision-requests"
const tenantWalletProvisionEventType = "billing.wallet.tenant.provision.requested.v1"
const tenantWalletProvisionStream = "billing:wallet:tenant:provision-requests"

const (
	billingOutboxClaimBatch       = 50
	billingOutboxFallbackInterval = 30 * time.Second
	billingOutboxFallbackJitter   = 10 * time.Second
	billingOutboxRetryMin         = time.Second
	billingOutboxRetryMax         = 30 * time.Second
)

var billingEventStreams = map[string]string{
	// [COMMENT]: Stream không đọc trực tiếp từ row; event mới phải được review và thêm vào allowlist này.
	personalWalletProvisionEventType: personalWalletProvisionStream,
	tenantWalletProvisionEventType:   tenantWalletProvisionStream,
}

type BillingOutboxRelay struct {
	repo        iamRepoInterface.BillingOutboxRepository
	sharedRedis *goredis.Client
	replicaAcks int
	durableWait time.Duration
	wake        chan struct{}
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewBillingOutboxRelay(
	repo iamRepoInterface.BillingOutboxRepository,
	sharedRedis *goredis.Client,
	replicaAcks int,
	durableWait time.Duration,
) (*BillingOutboxRelay, error) {
	if repo == nil || sharedRedis == nil {
		return nil, errors.New("iam billing relay requires repository and Shared Redis")
	}
	if replicaAcks < 0 || durableWait <= 0 {
		return nil, errors.New("iam billing relay requires non-negative replica acks and positive durable wait")
	}
	return &BillingOutboxRelay{
		repo:        repo,
		sharedRedis: sharedRedis,
		replicaAcks: replicaAcks,
		durableWait: durableWait,
		// [COMMENT]: Capacity 1 cố ý coalesce burst verify; outbox row mới là dữ liệu cần giữ, wake chỉ là hint.
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}, nil
}

// Notify đánh thức relay sau activation commit mà không chặn HTTP request.
func (r *BillingOutboxRelay) Notify() {
	if r == nil {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
		// [COMMENT]: Signal đang pending nên không cần enqueue thêm; reconciliation vẫn là safety net.
	}
}

func (r *BillingOutboxRelay) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go r.run(ctx)
}

func (r *BillingOutboxRelay) run(ctx context.Context) {
	defer close(r.done)

	// [COMMENT]: Startup drain bắt các row commit khi pod cũ chết trước wake hoặc trong thời gian rolling deploy.
	timer := time.NewTimer(0)
	defer timer.Stop()
	retryBackoff := billingOutboxRetryMin
	var retryNotBefore time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-timer.C:
		}

		// [COMMENT]: Wake burst trong lúc DB lỗi không được bypass exponential backoff và tạo retry storm.
		if remaining := time.Until(retryNotBefore); remaining > 0 {
			resetBillingOutboxTimer(timer, remaining)
			continue
		}

		if err := r.drain(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.SysError("iam.billing_outbox.claim", err.Error())
			retryNotBefore = time.Now().Add(retryBackoff)
			resetBillingOutboxTimer(timer, retryBackoff)
			retryBackoff *= 2
			if retryBackoff > billingOutboxRetryMax {
				retryBackoff = billingOutboxRetryMax
			}
			continue
		}

		retryNotBefore = time.Time{}
		retryBackoff = billingOutboxRetryMin
		resetBillingOutboxTimer(timer, billingOutboxFallbackDelay())
	}
}

func (r *BillingOutboxRelay) drain(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		events, err := r.repo.Claim(ctx, billingOutboxClaimBatch)
		if err != nil {
			return err
		}
		for _, event := range events {
			r.publish(ctx, event)
		}
		if len(events) < billingOutboxClaimBatch {
			return nil
		}
	}
}

func billingOutboxFallbackDelay() time.Duration {
	// [COMMENT]: Jitter lệch nhịp reconciliation giữa nhiều Controlplane pod, tránh cùng hit partial index.
	jitter := time.Duration(time.Now().UnixNano() % int64(billingOutboxFallbackJitter))
	return billingOutboxFallbackInterval + jitter
}

func resetBillingOutboxTimer(timer *time.Timer, delay time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(delay)
}

func (r *BillingOutboxRelay) publish(ctx context.Context, event iamEntity.BillingOutboxEvent) {
	stream, ok := billingEventStreams[event.EventType]
	if !ok || !validBillingEvent(event) {
		// [COMMENT]: Contract invalid là lỗi vĩnh viễn; đưa DEAD ngay để không đốt retry budget và gây noisy loop.
		_ = r.repo.MarkDead(ctx, event.ID, "unsupported or invalid billing event contract")
		return
	}

	// [COMMENT]: WAITAOF chỉ xác nhận các write trước đó trên cùng Redis connection,
	// vì vậy XADD và durability fence bắt buộc dùng một dedicated pooled connection.
	// Clone dùng chung pool nhưng nới read timeout riêng cho blocking WAITAOF, không làm
	// tăng latency ceiling của cache/pubsub command thông thường.
	conn := r.sharedRedis.WithTimeout(r.durableWait + time.Second).Conn()
	defer func() { _ = conn.Close() }()
	if err := conn.XAdd(ctx, &goredis.XAddArgs{
		Stream: stream,
		Values: map[string]any{
			"event_id":   event.EventID.String(),
			"event_type": event.EventType,
			"payload":    event.Payload,
		},
	}).Err(); err != nil {
		_ = r.repo.MarkFailed(ctx, event.ID, err.Error())
		return
	}
	waitCtx, cancel := context.WithTimeout(ctx, r.durableWait)
	persisted, err := conn.Do(
		waitCtx,
		"WAITAOF",
		1,
		r.replicaAcks,
		r.durableWait.Milliseconds(),
	).Slice()
	cancel()
	if err != nil || len(persisted) != 2 {
		if err == nil {
			err = errors.New("invalid WAITAOF response")
		}
		_ = r.repo.MarkFailed(ctx, event.ID, err.Error())
		return
	}
	localAOF, localOK := persisted[0].(int64)
	replicaAOF, replicaOK := persisted[1].(int64)
	if !localOK || !replicaOK || localAOF < 1 || replicaAOF < int64(r.replicaAcks) {
		// [COMMENT]: XADD có thể đã tồn tại nhưng chưa đạt durability policy. Giữ outbox
		// retry tạo at-least-once duplicate; Cost inbox event_id/hash chịu trách nhiệm dedupe.
		_ = r.repo.MarkFailed(
			ctx,
			event.ID,
			fmt.Sprintf(
				"Shared Redis durability fence not met: local=%v replicas=%v required=%d",
				persisted[0],
				persisted[1],
				r.replicaAcks,
			),
		)
		return
	}
	if err := r.repo.MarkPublished(ctx, event.ID); err != nil {
		logger.SysError("iam.billing_outbox.mark_published", err.Error())
	}
}

func validBillingEvent(event iamEntity.BillingOutboxEvent) bool {
	if event.EventID == uuid.Nil || event.OwnerID == uuid.Nil || (event.OwnerType != "PERSONAL" && event.OwnerType != "TENANT") {
		return false
	}
	switch event.EventType {
	case personalWalletProvisionEventType:
		wire := &iamproto.PersonalWalletProvisionRequestedV1{}
		if err := proto.Unmarshal(event.Payload, wire); err != nil {
			return false
		}
		wireEventID, eventErr := uuid.FromBytes(wire.GetEventId())
		wireOwnerID, ownerErr := uuid.FromBytes(wire.GetOwnerId())
		return eventErr == nil && ownerErr == nil && wireEventID == event.EventID &&
			wireOwnerID == event.OwnerID && wire.GetOwnerType() == event.OwnerType &&
			wire.GetSchemaVersion() == 1 && wire.GetCurrency() == "USD"
	case tenantWalletProvisionEventType:
		wire := &iamproto.TenantWalletProvisionRequestedV1{}
		if err := proto.Unmarshal(event.Payload, wire); err != nil {
			return false
		}
		wireEventID, eventErr := uuid.FromBytes(wire.GetEventId())
		wireTenantID, tenantErr := uuid.FromBytes(wire.GetTenantId())
		wireActorID, actorErr := uuid.FromBytes(wire.GetActorUserId())
		return eventErr == nil && tenantErr == nil && actorErr == nil &&
			wireEventID == event.EventID && wireTenantID == event.OwnerID &&
			wireActorID != uuid.Nil && event.OwnerType == "TENANT" &&
			wire.GetSchemaVersion() == 1 && wire.GetCurrency() == "USD"
	default:
		return false
	}
}

func (r *BillingOutboxRelay) Stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
	}
}
