/*
============================================================================
MAP: BILLING REPOSITORY IMPLEMENTATION - PRICING OUTBOX
============================================================================
CONTRACT:
1. Thực thi các truy vấn SQL nguyên tử trên PostgreSQL pool (*pgxpool.Pool).
2. Sử dụng SKIP LOCKED để hỗ trợ HA Worker Cluster.
============================================================================
*/

package repository

import (
	"context"
	"fmt"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pricingOutboxRepository struct {
	db *pgxpool.Pool
}

// [COMMENT]: NewPricingOutboxRepository khởi tạo instance thực thi interface PricingOutboxRepository.
func NewPricingOutboxRepository(db *pgxpool.Pool) billingRepoInterface.PricingOutboxRepository {
	return &pricingOutboxRepository{db: db}
}

// [COMMENT]: RefreshTierVersionStatuses tính toán và cập nhật trạng thái của các phiên bản bảng giá (ACTIVE/SCHEDULED/SUPERSEDED).
func (r *pricingOutboxRepository) RefreshTierVersionStatuses(ctx context.Context) error {
	_, err := r.db.Exec(ctx, `
		WITH projected AS (
			SELECT id, CASE
				WHEN effective_to IS NOT NULL AND effective_to <= NOW() THEN 'SUPERSEDED'
				WHEN effective_from <= NOW() AND (effective_to IS NULL OR NOW() < effective_to) THEN 'ACTIVE'
				ELSE 'SCHEDULED'
			END AS desired_status
			FROM billing.tier_versions
			WHERE status <> 'CANCELLED'
		)
		UPDATE billing.tier_versions version
		SET status = projected.desired_status
		FROM projected
		WHERE version.id = projected.id AND version.status IS DISTINCT FROM projected.desired_status
	`)
	if err != nil {
		return fmt.Errorf("pricing outbox repo: refresh version statuses failed: %w", err)
	}
	return nil
}

// [COMMENT]: GetUnpublishedOutboxBatch lấy đợt các bản ghi outbox chưa được phát sóng bằng FOR UPDATE SKIP LOCKED.
func (r *pricingOutboxRepository) GetUnpublishedOutboxBatch(ctx context.Context, limit int) ([]*entity.PricingOutboxRow, error) {

	rows, err := r.db.Query(ctx, `
		SELECT id, tier_id, tier_version_id, version_number, service_type, effective_from, checksum, occurred_at
		FROM billing.pricing_outbox
		WHERE published_at IS NULL
		ORDER BY occurred_at, id
		FOR UPDATE SKIP LOCKED
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("pricing outbox repo: select outbox batch failed: %w", err)
	}
	defer rows.Close()

	var batch []*entity.PricingOutboxRow
	for rows.Next() {
		var row entity.PricingOutboxRow
		var rawServiceType string
		if err := rows.Scan(
			&row.ID,
			&row.TierID,
			&row.TierVersionID,
			&row.VersionNumber,
			&rawServiceType,
			&row.EffectiveFrom,
			&row.Checksum,
			&row.OccurredAt,
		); err != nil {
			return nil, fmt.Errorf("pricing outbox repo: scan row failed: %w", err)
		}
		row.ServiceType = entity.ServiceType(rawServiceType)
		batch = append(batch, &row)
	}
	return batch, nil
}

// [COMMENT]: MarkOutboxPublished đánh dấu bản ghi outbox đã được phát sóng thành công sang NATS.
func (r *pricingOutboxRepository) MarkOutboxPublished(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `UPDATE billing.pricing_outbox SET published_at = NOW(), last_error = NULL WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("pricing outbox repo: mark published failed: %w", err)
	}
	return nil
}

// [COMMENT]: RecordOutboxError ghi nhận lỗi phát sóng và tăng số lần thử lại retry_count.
func (r *pricingOutboxRepository) RecordOutboxError(ctx context.Context, id uuid.UUID, errMsg string) error {
	_, err := r.db.Exec(ctx, `UPDATE billing.pricing_outbox SET retry_count = retry_count + 1, last_error = $1 WHERE id = $2`, errMsg, id)
	if err != nil {
		return fmt.Errorf("pricing outbox repo: record error failed: %w", err)
	}
	return nil
}
