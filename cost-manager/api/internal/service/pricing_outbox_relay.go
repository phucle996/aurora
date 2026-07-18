/*
============================================================================
MAP: BILLING SERVICE LAYER - PRICING OUTBOX RELAY
============================================================================
CONTRACT:
1. Điều phối PricingOutboxRepository để quét outbox rows và phát sang NATS Subject `billing.pricing.tier_version.published`.
2. Không thực thi SQL trực tiếp tại Service Layer.
3. Thực thi đợt relay batch inline trực tiếp trong vòng lặp ticker của Run().
============================================================================
*/

package service

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	billingRepoInterface "cost-manager/api/internal/domain/repo"
	pricingv1 "cost-manager/api/internal/genproto/billing/pricing/v1"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

const pricingVersionPublishedSubject = "billing.pricing.tier_version.published"

// PricingOutboxRelay điều phối công việc relay outbox sang NATS bằng cách gọi sang Repository Layer.
type PricingOutboxRelay struct {
	repo billingRepoInterface.PricingOutboxRepository
	nats *nats.Conn
	wake chan struct{}
}

// [COMMENT]: NewPricingOutboxRelay khởi tạo instance relay outbox cho bảng giá.
func NewPricingOutboxRelay(repo billingRepoInterface.PricingOutboxRepository, natsConn *nats.Conn) *PricingOutboxRelay {
	return &PricingOutboxRelay{repo: repo, nats: natsConn, wake: make(chan struct{}, 1)}
}

// [COMMENT]: Notify coalesce nhiều commit liên tiếp thành một wake; producer không bao giờ bị block sau commit DB.
func (r *PricingOutboxRelay) Notify() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// [COMMENT]: Run drain ngay lúc startup/commit; timer chậm chỉ là safety net khi process crash giữa commit và wake.
func (r *PricingOutboxRelay) Run(ctx context.Context) {
	reconcile := time.NewTimer(0)
	defer reconcile.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
			if err := r.drain(ctx, false); err != nil && ctx.Err() == nil {
				fmt.Printf("Pricing outbox relay error: %v\n", err)
			}
		case <-reconcile.C:
			if err := r.drain(ctx, true); err != nil && ctx.Err() == nil {
				fmt.Printf("Pricing outbox relay reconciliation error: %v\n", err)
			}
			// [COMMENT]: Jitter tránh mọi replica cùng đánh DB tại đúng một thời điểm.
			reconcile.Reset(30*time.Second + time.Duration(rand.Int63n(int64(10*time.Second))))
		}
	}
}

func (r *PricingOutboxRelay) drain(ctx context.Context, refreshStatuses bool) error {
	if refreshStatuses {
		// 1. Cập nhật trạng thái các phiên bản bảng giá qua Repo
		if err := r.repo.RefreshTierVersionStatuses(ctx); err != nil {
			return fmt.Errorf("refresh pricing version statuses failed: %w", err)
		}
	}

	for {
		// 2. Lấy đợt bản ghi outbox chưa được phát sóng
		batch, err := r.repo.GetUnpublishedOutboxBatch(ctx, 100)
		if err != nil {
			return fmt.Errorf("get unpublished outbox batch failed: %w", err)
		}

		if len(batch) == 0 {
			return nil
		}

		// 3. Duyệt danh sách bản ghi, marshal Protobuf và publish sang NATS
		var publishErr error
		for _, row := range batch {
			payload, marshalErr := proto.Marshal(&pricingv1.TierVersionPublished{
				EventId:             row.ID.String(),
				TierId:              row.TierID.String(),
				TierVersionId:       row.TierVersionID.String(),
				VersionNumber:       row.VersionNumber,
				ServiceType:         string(row.ServiceType),
				EffectiveFromUnixMs: row.EffectiveFrom.UnixMilli(),
				Checksum:            row.Checksum,
				OccurredAtUnixMs:    row.OccurredAt.UnixMilli(),
			})
			if marshalErr != nil {
				publishErr = fmt.Errorf("marshal outbox event %s failed: %w", row.ID, marshalErr)
				break
			}

			if err := r.nats.Publish(pricingVersionPublishedSubject, payload); err != nil {
				_ = r.repo.RecordOutboxError(ctx, row.ID, err.Error())
				publishErr = fmt.Errorf("publish outbox event %s failed: %w", row.ID, err)
				break
			}

			if err := r.repo.MarkOutboxPublished(ctx, row.ID); err != nil {
				publishErr = fmt.Errorf("mark outbox event %s published failed: %w", row.ID, err)
				break
			}
		}

		if publishErr != nil && ctx.Err() == nil {
			return publishErr
		}

		// 4. Flush các tin nhắn NATS sang network socket
		flushCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		if err := r.nats.FlushWithContext(flushCtx); err != nil && ctx.Err() == nil {
			cancel()
			return fmt.Errorf("flush pricing events failed: %w", err)
		}
		cancel()
	}
}
