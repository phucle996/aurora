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
}

// [COMMENT]: NewPricingOutboxRelay khởi tạo instance relay outbox cho bảng giá.
func NewPricingOutboxRelay(repo billingRepoInterface.PricingOutboxRepository, natsConn *nats.Conn) *PricingOutboxRelay {
	return &PricingOutboxRelay{repo: repo, nats: natsConn}
}

// [COMMENT]: Run định kỳ gọi repository quét các outbox row chưa phát sóng và publish sang NATS (xử lý inline).
func (r *PricingOutboxRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 1. Cập nhật trạng thái các phiên bản bảng giá qua Repo
			if err := r.repo.RefreshTierVersionStatuses(ctx); err != nil && ctx.Err() == nil {
				fmt.Printf("Pricing outbox relay error: refresh pricing version statuses failed: %v\n", err)
				continue
			}

			// 2. Lấy đợt bản ghi outbox chưa được phát sóng
			batch, err := r.repo.GetUnpublishedOutboxBatch(ctx, 100)
			if err != nil && ctx.Err() == nil {
				fmt.Printf("Pricing outbox relay error: get unpublished outbox batch failed: %v\n", err)
				continue
			}

			if len(batch) == 0 {
				continue
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
				fmt.Printf("Pricing outbox relay error: %v\n", publishErr)
				continue
			}

			// 4. Flush các tin nhắn NATS sang network socket
			flushCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			if err := r.nats.FlushWithContext(flushCtx); err != nil && ctx.Err() == nil {
				fmt.Printf("Pricing outbox relay error: flush pricing events failed: %v\n", err)
			}
			cancel()
		}
	}

}
