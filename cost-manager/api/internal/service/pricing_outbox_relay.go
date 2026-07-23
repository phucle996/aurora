/*
============================================================================
MAP: BILLING SERVICE LAYER - PRICING OUTBOX RELAY
============================================================================
CONTRACT:
1. Điều phối PricingOutboxRepository để quét outbox rows và phát hint sang Shared Redis
   PubSub channel `billing.pricing.tier_version.published`.
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

	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const pricingVersionPublishedChannel = "billing.pricing.tier_version.published"

// PricingOutboxRelay điều phối công việc relay outbox sang Shared Redis PubSub.
type PricingOutboxRelay struct {
	repo        billingRepoInterface.PricingOutboxRepository
	sharedRedis *goredis.Client
	wake        chan struct{}
}

// [COMMENT]: NewPricingOutboxRelay khởi tạo instance relay outbox cho bảng giá.
func NewPricingOutboxRelay(repo billingRepoInterface.PricingOutboxRepository, sharedRedis *goredis.Client) *PricingOutboxRelay {
	return &PricingOutboxRelay{repo: repo, sharedRedis: sharedRedis, wake: make(chan struct{}, 1)}
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

		// 3. Duyệt danh sách bản ghi, marshal Protobuf và publish hint sang Shared Redis.
		// DB outbox vẫn là SoT; Engine cold-start/reconciler tự load lại nếu PubSub bị lỡ.
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

			// [COMMENT]: Không mark published khi không có listener nào nhận hint.
			// Engine có thể đang rolling restart; row sẽ được retry ở reconciliation sau đó.
			listeners, err := r.sharedRedis.Publish(ctx, pricingVersionPublishedChannel, payload).Result()
			if err != nil {
				_ = r.repo.RecordOutboxError(ctx, row.ID, err.Error())
				publishErr = fmt.Errorf("publish outbox event %s failed: %w", row.ID, err)
				break
			}
			if listeners == 0 {
				err := fmt.Errorf("no pricing listener subscribed to %s", pricingVersionPublishedChannel)
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

	}
}
