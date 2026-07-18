package iamSvcImpl

import (
	"context"
	"fmt"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
)

const personalWalletProvisionEventType = "billing.wallet.personal.provision.requested.v1"

const (
	billingOutboxClaimBatch       = 50
	billingOutboxFallbackInterval = 30 * time.Second
	billingOutboxFallbackJitter   = 10 * time.Second
	billingOutboxRetryMin         = time.Second
	billingOutboxRetryMax         = 30 * time.Second
)

var billingEventSubjects = map[string]string{
	// [COMMENT]: Subject không đọc trực tiếp từ row; event mới phải được review và thêm vào allowlist này.
	personalWalletProvisionEventType: personalWalletProvisionEventType,
}

type BillingOutboxRelay struct {
	repo   iamRepoInterface.BillingOutboxRepository
	js     jetstream.JetStream
	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

func NewBillingOutboxRelay(repo iamRepoInterface.BillingOutboxRepository, nc *nats.Conn) (*BillingOutboxRelay, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name: "BILLING_DOMAIN_EVENTS", Subjects: []string{personalWalletProvisionEventType},
		Storage: jetstream.FileStorage, Retention: jetstream.LimitsPolicy, MaxAge: 30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("iam billing relay: ensure stream: %w", err)
	}
	return &BillingOutboxRelay{
		repo: repo,
		js:   js,
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
	subject, ok := billingEventSubjects[event.EventType]
	if !ok || !validBillingEvent(event) {
		// [COMMENT]: Contract invalid là lỗi vĩnh viễn; đưa DEAD ngay để không đốt retry budget và gây noisy loop.
		_ = r.repo.MarkDead(ctx, event.ID, "unsupported or invalid billing event contract")
		return
	}

	// [COMMENT]: PubAck là ranh giới broker đã persist; Msg-Id giữ retry sau lease theo at-least-once nhưng idempotent.
	_, err := r.js.Publish(ctx, subject, event.Payload, jetstream.WithMsgID(event.EventID.String()))
	if err != nil {
		_ = r.repo.MarkFailed(ctx, event.ID, err.Error())
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
