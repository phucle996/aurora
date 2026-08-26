package iamStream

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamproto "controlplane/internal/iam/transport/proto"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	deviceRuntimeRevokeStream           = "iam:device:revoke-requests"
	deviceRuntimeRevokeClaimBatch       = 50
	deviceRuntimeRevokeLease            = 30 * time.Second
	deviceRuntimeRevokeFallbackInterval = 30 * time.Second
	deviceRuntimeRevokeFallbackJitter   = 10 * time.Second
	deviceRuntimeRevokeRetryMin         = time.Second
	deviceRuntimeRevokeRetryMax         = 30 * time.Second
)

// DeviceRuntimeRevokeRelay is the transport side of the device-runtime-revoke
// workflow. Its wake channel is only a hint; the outbox row is the authority.
type DeviceRuntimeRevokeRelay struct {
	service     iamSvcInterface.DeviceRuntimeRevokeService
	sharedRedis *goredis.Client
	replicaAcks int
	durableWait time.Duration
	wake        chan struct{}

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func NewDeviceRuntimeRevokeRelay(
	service iamSvcInterface.DeviceRuntimeRevokeService,
	sharedRedis *goredis.Client,
	replicaAcks int,
	durableWait time.Duration,
) (*DeviceRuntimeRevokeRelay, error) {
	if replicaAcks < 0 || durableWait <= 0 || durableWait+time.Second >= deviceRuntimeRevokeLease {
		return nil, errors.New("iam device runtime revoke relay durability deadline must fit inside the outbox lease")
	}
	return &DeviceRuntimeRevokeRelay{
		service:     service,
		sharedRedis: sharedRedis,
		replicaAcks: replicaAcks,
		durableWait: durableWait,
		wake:        make(chan struct{}, 1),
	}, nil
}

// Notify schedules an immediate drain after the CTE has committed. The worker
// always also reconciles periodically, so a dropped wakeup cannot lose a revoke.
func (r *DeviceRuntimeRevokeRelay) Notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *DeviceRuntimeRevokeRelay) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(pkgcontext.WithOperation(context.Background(), "iam.device.runtime_revoke.relay"))
	r.cancel = cancel
	r.done = make(chan struct{})
	go r.run(ctx, r.done)
}

func (r *DeviceRuntimeRevokeRelay) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	timer := time.NewTimer(0)
	defer timer.Stop()

	retry := deviceRuntimeRevokeRetryMin
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
			logger.SysErrorCtx(ctx, "iam.device.runtime_revoke.claim", err.Error())
			r.resetTimer(timer, retry)
			retry *= 2
			if retry > deviceRuntimeRevokeRetryMax {
				retry = deviceRuntimeRevokeRetryMax
			}
			continue
		}

		retry = deviceRuntimeRevokeRetryMin
		r.resetTimer(
			timer,
			deviceRuntimeRevokeFallbackInterval+time.Duration(rand.Int64N(int64(deviceRuntimeRevokeFallbackJitter))),
		)
	}
}

func (r *DeviceRuntimeRevokeRelay) resetTimer(timer *time.Timer, after time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(after)
}

func (r *DeviceRuntimeRevokeRelay) drain(ctx context.Context) error {
	for {
		events, err := r.service.Claim(ctx, deviceRuntimeRevokeClaimBatch)
		if err != nil {
			return err
		}
		for _, event := range events {
			r.publish(ctx, event)
		}
		if len(events) < deviceRuntimeRevokeClaimBatch {
			return nil
		}
	}
}

func (r *DeviceRuntimeRevokeRelay) publish(ctx context.Context, event iamEntity.DeviceRuntimeRevokeOutboxEvent) {
	if event.EventID == uuid.Nil || event.UserID == uuid.Nil || len(event.ClientDeviceIDs) == 0 || len(event.ClientDeviceIDs) > deviceRuntimeRevokeClaimBatch {
		_ = r.service.MarkDead(ctx, event.ID, "invalid device runtime revoke outbox event")
		return
	}

	seen := make(map[uuid.UUID]struct{}, len(event.ClientDeviceIDs))
	for _, rawDeviceID := range event.ClientDeviceIDs {
		deviceID, err := uuid.Parse(rawDeviceID)
		if err != nil || deviceID == uuid.Nil {
			_ = r.service.MarkDead(ctx, event.ID, "invalid device runtime revoke device identifier")
			return
		}
		if _, duplicate := seen[deviceID]; duplicate {
			_ = r.service.MarkDead(ctx, event.ID, "duplicate device runtime revoke identifier")
			return
		}
		seen[deviceID] = struct{}{}
	}

	payload, err := proto.Marshal(&iamproto.RevokeUserSessionsByDevicesRequest{
		UserId:          event.UserID.String(),
		ClientDeviceIds: event.ClientDeviceIDs,
	})
	if err != nil {
		_ = r.service.MarkDead(ctx, event.ID, "cannot marshal device runtime revoke protobuf")
		return
	}

	conn := r.sharedRedis.WithTimeout(r.durableWait + time.Second).Conn()
	defer func() { _ = conn.Close() }()
	if err := conn.XAdd(ctx, &goredis.XAddArgs{
		Stream: deviceRuntimeRevokeStream,
		Values: map[string]any{
			"event_id": event.EventID.String(),
			"payload":  payload,
		},
	}).Err(); err != nil {
		_ = r.service.MarkFailed(ctx, event.ID, err.Error())
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
		_ = r.service.MarkFailed(ctx, event.ID, err.Error())
		return
	}

	localAOF, localOK := persisted[0].(int64)
	replicaAOF, replicaOK := persisted[1].(int64)
	if !localOK || !replicaOK || localAOF < 1 || replicaAOF < int64(r.replicaAcks) {
		_ = r.service.MarkFailed(
			ctx,
			event.ID,
			fmt.Sprintf("Shared Redis durability fence not met: local=%v replicas=%v required=%d", persisted[0], persisted[1], r.replicaAcks),
		)
		return
	}
	if err := r.service.MarkPublished(ctx, event.ID); err != nil {
		logger.SysErrorCtx(ctx, "iam.device.runtime_revoke.mark_published", err.Error())
	}
}

func (r *DeviceRuntimeRevokeRelay) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	r.cancel = nil
	r.done = nil
	r.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		logger.SysWarn("iam.device.runtime_revoke.stop", "timed out waiting for device runtime revoke relay")
	}
}
