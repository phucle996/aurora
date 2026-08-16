package repository

import (
	"context"
	"fmt"

	"cost-manager/api/internal/domain/entity"

	"github.com/jackc/pgx/v5/pgxpool"
)

type mailZoneAdjustmentListRepository struct{ db *pgxpool.Pool }

func NewMailZoneAdjustmentListRepository(db *pgxpool.Pool) *mailZoneAdjustmentListRepository {
	return &mailZoneAdjustmentListRepository{db: db}
}

func (r *mailZoneAdjustmentListRepository) ListMailZonePriceAdjustments(ctx context.Context, query entity.MailZoneAdjustmentListQuery) ([]entity.MailZoneAdjustmentListItem, bool, error) {
	rows, err := r.db.Query(ctx, `
		WITH history AS (
			SELECT id, zone_id, version_number, status, effective_from, effective_to,
			       multiplier_numerator, multiplier_denominator, checksum, change_reason,
			       created_by, created_at,
			       version_number = MAX(version_number) OVER () AS is_latest,
			       effective_from <= NOW() AND (effective_to IS NULL OR NOW() < effective_to) AS is_effective
			FROM billing.mail_zone_price_adjustment_versions
			WHERE zone_id=$1
		), bounded AS (
			SELECT * FROM history ORDER BY version_number DESC LIMIT $2
		)
		SELECT id, zone_id, version_number, status, effective_from, effective_to,
		       multiplier_numerator, multiplier_denominator, checksum, change_reason,
		       created_by, created_at, is_latest, is_effective
		FROM bounded ORDER BY version_number DESC
	`, query.ZoneID, query.Limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("Mail Zone adjustment list repo: query: %w", err)
	}
	defer rows.Close()
	items := make([]entity.MailZoneAdjustmentListItem, 0, query.Limit+1)
	for rows.Next() {
		var item entity.MailZoneAdjustmentListItem
		if err := rows.Scan(
			&item.ID, &item.ZoneID, &item.VersionNumber, &item.Status,
			&item.EffectiveFrom, &item.EffectiveTo, &item.MultiplierNumerator,
			&item.MultiplierDenominator, &item.Checksum, &item.ChangeReason,
			&item.CreatedBy, &item.CreatedAt, &item.IsLatest, &item.IsEffective,
		); err != nil {
			return nil, false, fmt.Errorf("Mail Zone adjustment list repo: scan: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("Mail Zone adjustment list repo: rows: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return items, hasMore, nil
}
