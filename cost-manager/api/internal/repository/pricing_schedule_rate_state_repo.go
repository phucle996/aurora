package repository

import (
	"context"
	"fmt"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/jackc/pgx/v5/pgxpool"
)

type pricingScheduleRateStateRepository struct {
	db *pgxpool.Pool
}

func NewPricingScheduleRateStateRepository(db *pgxpool.Pool) billingRepoInterface.PricingScheduleRateStateRepository {
	return &pricingScheduleRateStateRepository{db: db}
}

func (r *pricingScheduleRateStateRepository) GetPricingScheduleRateState(ctx context.Context, code string) ([]entity.PricingScheduleRateStateRow, error) {
	rows, err := r.db.Query(ctx, `
		WITH observed AS (SELECT NOW() AS at),
		target AS (
			SELECT s.id, s.code, s.display_name, s.charge_kind_code, s.pricing_model,
			       s.currency, s.metadata_version
			FROM billing.pricing_schedules s WHERE s.code=$1
		), latest AS (
			SELECT MAX(v.version_number) AS version_number
			FROM billing.pricing_schedule_versions v JOIN target t ON t.id=v.pricing_schedule_id
			WHERE v.status <> 'CANCELLED'
		), effective AS (
			SELECT v.id, 'EFFECTIVE'::text AS role, v.version_number, v.status, v.effective_from,
			       v.effective_to, v.checksum, v.change_reason
			FROM billing.pricing_schedule_versions v JOIN target t ON t.id=v.pricing_schedule_id CROSS JOIN observed o
			WHERE v.status <> 'CANCELLED' AND v.effective_from <= o.at
			  AND (v.effective_to IS NULL OR v.effective_to > o.at)
			ORDER BY v.effective_from DESC, v.version_number DESC LIMIT 1
		), next_scheduled AS (
			SELECT v.id, 'NEXT_SCHEDULED'::text AS role, v.version_number, v.status, v.effective_from,
			       v.effective_to, v.checksum, v.change_reason
			FROM billing.pricing_schedule_versions v JOIN target t ON t.id=v.pricing_schedule_id CROSS JOIN observed o
			WHERE v.status <> 'CANCELLED' AND v.effective_from > o.at
			ORDER BY v.effective_from ASC, v.version_number ASC LIMIT 1
		), selected AS (
			SELECT * FROM effective UNION ALL SELECT * FROM next_scheduled
		)
		SELECT t.id, t.code, t.display_name, t.charge_kind_code, t.pricing_model, t.currency, t.metadata_version,
		       o.at, l.version_number,
		       s.role, s.id, s.version_number, s.status, s.effective_from, s.effective_to, s.checksum, s.change_reason,
		       b.id, b.range_start_quantity, b.range_end_quantity, b.price_numerator_micro_units, b.price_denominator_quantity
		FROM target t CROSS JOIN observed o CROSS JOIN latest l
		LEFT JOIN selected s ON TRUE
		LEFT JOIN billing.pricing_schedule_scalar_brackets b ON b.pricing_schedule_version_id=s.id
		ORDER BY CASE s.role WHEN 'EFFECTIVE' THEN 0 WHEN 'NEXT_SCHEDULED' THEN 1 ELSE 2 END, b.range_start_quantity`, code)
	if err != nil {
		return nil, fmt.Errorf("pricing schedule rate state repo: query: %w", err)
	}
	defer rows.Close()

	result := make([]entity.PricingScheduleRateStateRow, 0)
	for rows.Next() {
		var row entity.PricingScheduleRateStateRow
		var chargeKind, model string
		if err := rows.Scan(
			&row.ScheduleID, &row.Code, &row.DisplayName, &chargeKind, &model, &row.Currency, &row.MetadataVersion,
			&row.ObservedAt, &row.LatestVersionNumber,
			&row.VersionRole, &row.VersionID, &row.VersionNumber, &row.VersionStatus, &row.EffectiveFrom, &row.EffectiveTo, &row.Checksum, &row.ChangeReason,
			&row.BracketID, &row.RangeStartQuantity, &row.RangeEndQuantity, &row.PriceNumerator, &row.PriceDenominator,
		); err != nil {
			return nil, fmt.Errorf("pricing schedule rate state repo: scan: %w", err)
		}
		row.ChargeKindCode = entity.ChargeKindCode(chargeKind)
		row.PricingModel = entity.PricingModel(model)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pricing schedule rate state repo: rows: %w", err)
	}
	if len(result) == 0 {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	}
	return result, nil
}

var _ billingRepoInterface.PricingScheduleRateStateRepository = (*pricingScheduleRateStateRepository)(nil)
