package hierarchySvcImpl

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	iamproto "controlplane/internal/iam/transport/proto"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	tenantWalletProvisionRequestedStream  = "billing:tenant-wallet:provision:requested:v1"
	tenantWalletProvisionClaimBatch       = 50
	tenantWalletProvisionLeaseDuration    = 30 * time.Second
	tenantWalletProvisionStartupJitter    = 2 * time.Second
	tenantWalletProvisionFallbackInterval = 30 * time.Second
	tenantWalletProvisionFallbackJitter   = 10 * time.Second
	tenantWalletProvisionRetryMin         = time.Second
	tenantWalletProvisionRetryMax         = 30 * time.Second
)

// TenantWalletProvisionRelay owns the durable tenant wallet-provision command
// from Hierarchy to Cost. Its repository methods are event-type fenced, so it
// cannot claim or mutate another future workflow in cost_outbox_records.
type TenantWalletProvisionRelay struct {
	repo        hierarchyRepoInterface.TenantRepository
	sharedRedis *goredis.Client
	replicaAcks int
	durableWait time.Duration
	wake        chan struct{}
	cancel      context.CancelFunc
	done        chan struct{}
}

func NewTenantWalletProvisionRelay(
	repo hierarchyRepoInterface.TenantRepository,
	sharedRedis *goredis.Client,
	replicaAcks int,
	durableWait time.Duration,
) (*TenantWalletProvisionRelay, error) {
	if repo == nil || sharedRedis == nil {
		return nil, errors.New("tenant wallet provision relay requires repository and Shared Redis")
	}
	if replicaAcks < 0 || durableWait <= 0 {
		return nil, errors.New("tenant wallet provision relay requires non-negative replica acks and positive durable wait")
	}
	if durableWait+time.Second >= tenantWalletProvisionLeaseDuration {
		return nil, errors.New("tenant wallet provision relay durable wait must remain below its claim lease")
	}
	return &TenantWalletProvisionRelay{
		repo:        repo,
		sharedRedis: sharedRedis,
		replicaAcks: replicaAcks,
		durableWait: durableWait,
		wake:        make(chan struct{}, 1),
		done:        make(chan struct{}),
	}, nil
}

func (r *TenantWalletProvisionRelay) Notify() {
	if r == nil {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *TenantWalletProvisionRelay) Start() {
	if r == nil || r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(pkgcontext.WithOperation(context.Background(), "hierarchy.tenant_wallet_provision.relay"))
	r.cancel = cancel
	go r.run(ctx)
}

func (r *TenantWalletProvisionRelay) run(ctx context.Context) {
	defer close(r.done)

	// Stagger startup drains during rolling deployment. The outbox is durable;
	// a bounded 0-2s delay avoids every new pod scanning the same index at once.
	timer := time.NewTimer(time.Duration(rand.Int64N(int64(tenantWalletProvisionStartupJitter))))
	defer timer.Stop()
	retryBackoff := tenantWalletProvisionRetryMin

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-timer.C:
		}

		if err := r.drain(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.SysErrorCtx(ctx, "hierarchy.tenant_wallet_provision.claim", err.Error())
			timer.Reset(retryBackoff)
			retryBackoff *= 2
			if retryBackoff > tenantWalletProvisionRetryMax {
				retryBackoff = tenantWalletProvisionRetryMax
			}
			continue
		}

		retryBackoff = tenantWalletProvisionRetryMin
		timer.Reset(tenantWalletProvisionFallbackInterval + time.Duration(rand.Int64N(int64(tenantWalletProvisionFallbackJitter))))
	}
}

func (r *TenantWalletProvisionRelay) drain(ctx context.Context) error {
	for {
		events, err := r.repo.ClaimTenantWalletProvisionOutbox(ctx, tenantWalletProvisionClaimBatch)
		if err != nil {
			return err
		}
		for _, event := range events {
			r.publish(ctx, event)
		}
		if len(events) < tenantWalletProvisionClaimBatch {
			return nil
		}
	}
}

func (r *TenantWalletProvisionRelay) publish(ctx context.Context, event hierarchyEntity.TenantWalletProvisionOutbox) {
	if event.EventID == uuid.Nil || event.TenantID == uuid.Nil || event.ActorUserID == uuid.Nil {
		_ = r.repo.MarkTenantWalletProvisionDead(ctx, event.ID, "invalid tenant wallet provision envelope")
		return
	}

	wire := &iamproto.TenantWalletProvisionRequestedV1{}
	if err := proto.Unmarshal(event.Payload, wire); err != nil {
		_ = r.repo.MarkTenantWalletProvisionDead(ctx, event.ID, "invalid tenant wallet provision protobuf payload")
		return
	}
	wireEventID, eventErr := uuid.FromBytes(wire.GetEventId())
	wireTenantID, tenantErr := uuid.FromBytes(wire.GetTenantId())
	wireActorID, actorErr := uuid.FromBytes(wire.GetActorUserId())
	_, occurredErr := time.Parse(time.RFC3339Nano, wire.GetOccurredAt())
	if eventErr != nil || tenantErr != nil || actorErr != nil || occurredErr != nil ||
		wireEventID != event.EventID || wireTenantID != event.TenantID || wireActorID != event.ActorUserID ||
		wire.GetCurrency() != "USD" || wire.GetSchemaVersion() != 1 {
		_ = r.repo.MarkTenantWalletProvisionDead(ctx, event.ID, "invalid tenant wallet provision contract")
		return
	}

	// The complete Redis publication is bounded below the 30s claim lease. If
	// it expires, the row returns to PENDING and another pod can safely retry.
	publishDeadline := r.durableWait + time.Second
	publishCtx, cancel := context.WithTimeout(ctx, publishDeadline)
	defer cancel()
	conn := r.sharedRedis.WithTimeout(publishDeadline).Conn()
	defer func() { _ = conn.Close() }()
	if err := conn.XAdd(publishCtx, &goredis.XAddArgs{
		Stream: tenantWalletProvisionRequestedStream,
		Values: map[string]any{
			"event_id":   event.EventID.String(),
			"event_type": "billing.tenant_wallet.provision.requested.v1",
			"payload":    event.Payload,
		},
	}).Err(); err != nil {
		_ = r.repo.MarkTenantWalletProvisionFailed(ctx, event.ID, err.Error())
		return
	}

	persisted, err := conn.Do(publishCtx, "WAITAOF", 1, r.replicaAcks, r.durableWait.Milliseconds()).Slice()
	if err != nil || len(persisted) != 2 {
		if err == nil {
			err = errors.New("invalid WAITAOF response")
		}
		_ = r.repo.MarkTenantWalletProvisionFailed(ctx, event.ID, err.Error())
		return
	}

	localAOF, localOK := persisted[0].(int64)
	replicaAOF, replicaOK := persisted[1].(int64)
	if !localOK || !replicaOK || localAOF < 1 || replicaAOF < int64(r.replicaAcks) {
		_ = r.repo.MarkTenantWalletProvisionFailed(ctx, event.ID, fmt.Sprintf("Shared Redis durability fence not met: local=%v replicas=%v required=%d", persisted[0], persisted[1], r.replicaAcks))
		return
	}

	if err := r.repo.MarkTenantWalletProvisionPublished(ctx, event.ID); err != nil {
		logger.SysErrorCtx(ctx, "hierarchy.tenant_wallet_provision.mark_published", fmt.Sprintf("event=%s: %v", event.EventID, err))
	}
}

func (r *TenantWalletProvisionRelay) Stop() {
	if r == nil || r.cancel == nil {
		return
	}
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		logger.SysWarn("hierarchy.tenant_wallet_provision.stop", "timed out waiting for tenant wallet provision relay")
	}
}
