/*
============================================================================
MAP: BILLING SERVICE LAYER - PRICING OUTBOX RELAY
============================================================================
CONTRACT:
1. Điều phối PricingOutboxRepository để quét outbox rows và phát hint sang Shared Redis
   PubSub channel `billing.pricing.schedule.version.published`.
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
	"cost-manager/api/pkg/logger"

	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const pricingVersionPublishedChannel = "billing.pricing.schedule.version.published"

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
		refreshStatuses := false
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		case <-reconcile.C:
			refreshStatuses = true
		}

		var relayErr error
		if refreshStatuses {
			if err := r.repo.RefreshPricingScheduleVersionStatuses(ctx); err != nil {
				relayErr = fmt.Errorf("refresh pricing version statuses failed: %w", err)
			}
		}

		for relayErr == nil {
			batch, err := r.repo.GetUnpublishedOutboxBatch(ctx, 100)
			if err != nil {
				relayErr = fmt.Errorf("get unpublished outbox batch failed: %w", err)
				break
			}

			if len(batch) == 0 {
				break
			}

			// DB outbox vẫn là SoT; Engine cold-start/reconciler tự load lại nếu PubSub bị lỡ.
			for _, row := range batch {
				zoneID := ""
				if row.ZoneID != nil {
					zoneID = row.ZoneID.String()
				}
				payload, marshalErr := proto.Marshal(&pricingv1.PricingScheduleVersionPublished{
					EventId:                  row.ID.String(),
					PricingScheduleId:        row.PricingScheduleID.String(),
					PricingScheduleVersionId: row.VersionID.String(),
					VersionNumber:            row.VersionNumber,
					ChargeKindCode:           string(row.ChargeKindCode),
					EffectiveFromUnixMs:      row.EffectiveFrom.UnixMilli(),
					Checksum:                 row.Checksum,
					OccurredAtUnixMs:         row.OccurredAt.UnixMilli(),
					ScopeType:                string(row.ScopeType),
					ZoneId:                   zoneID,
				})
				if marshalErr != nil {
					relayErr = fmt.Errorf("marshal outbox event %s failed: %w", row.ID, marshalErr)
					break
				}

				// [COMMENT]: Không mark published khi không có listener nào nhận hint.
				// Engine có thể đang rolling restart; row sẽ được retry ở reconciliation sau đó.
				_, publishErr := r.sharedRedis.Publish(ctx, pricingVersionPublishedChannel, payload).Result()
				if publishErr != nil {
					_ = r.repo.RecordOutboxError(ctx, row.ID, publishErr.Error())
					relayErr = fmt.Errorf("publish outbox event %s failed: %w", row.ID, publishErr)
					break
				}

				// Cache invalidation is a separate best-effort hint so API subscribers do not
				// affect the Engine listener count used by the existing outbox safety check.
				if err := r.sharedRedis.Publish(ctx, pricingCacheInvalidationChannel, payload).Err(); err != nil {
					logger.SysWarn("billing.pricing.cache.invalidate.publish", err.Error())
				}

				if err := r.repo.MarkOutboxPublished(ctx, row.ID); err != nil {
					relayErr = fmt.Errorf("mark outbox event %s published failed: %w", row.ID, err)
					break
				}
			}

			if relayErr != nil {
				break
			}
		}
		if relayErr != nil && ctx.Err() == nil {
			logger.SysError("billing.pricing.outbox.relay", relayErr.Error())
		}
		if refreshStatuses {
			// [COMMENT]: Tính interval từ lúc đợt reconciliation hoàn tất để relay chậm không tạo vòng lặp nóng.
			reconcile.Reset(30*time.Second + time.Duration(rand.Int63n(int64(10*time.Second))))
		}
	}
}
