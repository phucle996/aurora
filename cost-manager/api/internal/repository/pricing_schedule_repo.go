package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pricingScheduleRepository owns Global catalog list/detail/snapshot reads and
// metadata updates. Product modules own their version-publish transactions.
// Source of Truth:
// - god_view/billing/billing_pricing_schedule_list_god_view.md
// - god_view/billing/billing_pricing_schedule_detail_god_view.md
// - god_view/billing/billing_pricing_schedule_metadata_update_god_view.md
type pricingScheduleRepository struct {
	db *pgxpool.Pool
}

// NewPricingScheduleRepository khởi tạo pricingScheduleRepository với PostgreSQL connection pool.
func NewPricingScheduleRepository(db *pgxpool.Pool) billingRepoInterface.PricingScheduleRepository {
	return &pricingScheduleRepository{db: db}
}

// ListPricingSchedules lấy danh sách các bảng giá theo phân trang, hỗ trợ lọc theo charge_kind_code và tìm kiếm theo code/display_name.
// Sử dụng CTE và Window Function COUNT(*) OVER() để tính tổng số dòng trong 1 query duy nhất.
func (r *pricingScheduleRepository) ListPricingSchedules(
	ctx context.Context,
	page, limit int,
	chargeKind entity.ChargeKindCode,
	search string,
) ([]*entity.PricingScheduleListItem, int64, error) {
	offset := (page - 1) * limit
	pattern := ""
	if search != "" {
		pattern = "%" + search + "%"
	}

	rows, err := r.db.Query(ctx, `
		WITH filtered AS (
			SELECT id, code, display_name, charge_kind_code, pricing_model,
			       currency, metadata_version, status, created_at, updated_at
			FROM billing.pricing_schedules
			WHERE ($1='' OR charge_kind_code=$1) AND ($2='' OR code ILIKE $3 OR display_name ILIKE $3)
		)
		SELECT *, COUNT(*) OVER() AS total_count
		FROM filtered
		ORDER BY created_at DESC, code ASC
		LIMIT $4 OFFSET $5`,
		string(chargeKind), search, pattern, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("pricing schedule repo: list: %w", err)
	}
	defer rows.Close()

	result := make([]*entity.PricingScheduleListItem, 0)
	var total int64

	for rows.Next() {
		var schedule entity.PricingScheduleListItem
		var chargeKindStr, model string

		if err := rows.Scan(
			&schedule.ID,
			&schedule.Code,
			&schedule.DisplayName,
			&chargeKindStr,
			&model,
			&schedule.Currency,
			&schedule.MetadataVersion,
			&schedule.Status,
			&schedule.CreatedAt,
			&schedule.UpdatedAt,
			&total,
		); err != nil {
			return nil, 0, fmt.Errorf("pricing schedule repo: scan: %w", err)
		}

		schedule.ChargeKindCode = entity.ChargeKindCode(chargeKindStr)
		schedule.PricingModel = entity.PricingModel(model)
		result = append(result, &schedule)
	}

	// Trường hợp page > 1 và không có dòng nào ở trang hiện tại, query lại count để trả về đúng total
	if len(result) == 0 && page > 1 {
		if err := r.db.QueryRow(ctx, `
			WITH filtered AS (
				SELECT 1 FROM billing.pricing_schedules
				WHERE ($1='' OR charge_kind_code=$1) AND ($2='' OR code ILIKE $3 OR display_name ILIKE $3)
			)
			SELECT COUNT(*) FROM filtered`,
			string(chargeKind), search, pattern,
		).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("pricing schedule repo: count empty page: %w", err)
		}
	}

	return result, total, rows.Err()
}

// GetPricingScheduleDetail lấy thông tin chi tiết của một bảng giá cùng phiên bản mới nhất và các bracket giá tương ứng.
func (r *pricingScheduleRepository) GetPricingScheduleDetail(
	ctx context.Context,
	code string,
) (*entity.PricingScheduleDetail, []entity.PricingScheduleDetailBracket, error) {
	rows, err := r.db.Query(ctx, `
		WITH target AS (
			SELECT id, code, display_name, charge_kind_code, pricing_model,
			       currency, metadata_version, status, created_at, updated_at
			FROM billing.pricing_schedules WHERE code=$1
		), latest AS (
			SELECT v.* FROM billing.pricing_schedule_versions v
			JOIN target t ON t.id=v.pricing_schedule_id
			ORDER BY v.version_number DESC LIMIT 1
		)
		SELECT t.id, t.code, t.display_name, t.charge_kind_code, t.pricing_model,
		       t.currency, t.metadata_version, t.status, t.created_at, t.updated_at,
		       v.id, v.version_number, v.pricing_model, v.status, v.effective_from, v.effective_to, v.checksum,
		       b.id, b.range_start_quantity, b.range_end_quantity, b.price_numerator_micro_units, b.price_denominator_quantity
		FROM target t
		LEFT JOIN latest v ON TRUE
		LEFT JOIN billing.pricing_schedule_scalar_brackets b ON b.pricing_schedule_version_id=v.id
		ORDER BY b.range_start_quantity`, code)
	if err != nil {
		return nil, nil, fmt.Errorf("pricing schedule repo: detail: %w", err)
	}
	defer rows.Close()

	var detail *entity.PricingScheduleDetail
	var brackets []entity.PricingScheduleDetailBracket

	for rows.Next() {
		var chargeKindStr, model string
		var versionModel, versionStatus, checksum *string
		var versionID *uuid.UUID
		var versionNumber *int
		var effectiveFrom, effectiveTo *time.Time
		var bracketID *uuid.UUID
		var start, end *int64
		var numerator, denominator *int64
		var row entity.PricingScheduleDetail

		if err := rows.Scan(
			&row.ID,
			&row.Code,
			&row.DisplayName,
			&chargeKindStr,
			&model,
			&row.Currency,
			&row.MetadataVersion,
			&row.Status,
			&row.CreatedAt,
			&row.UpdatedAt,
			&versionID,
			&versionNumber,
			&versionModel,
			&versionStatus,
			&effectiveFrom,
			&effectiveTo,
			&checksum,
			&bracketID,
			&start,
			&end,
			&numerator,
			&denominator,
		); err != nil {
			return nil, nil, fmt.Errorf("pricing schedule repo: detail scan: %w", err)
		}

		if detail == nil {
			row.ChargeKindCode = entity.ChargeKindCode(chargeKindStr)
			row.PricingModel = entity.PricingModel(model)
			if versionID != nil {
				row.HasLatestVersion = true
				row.LatestVersionID = *versionID
				row.LatestVersionNumber = *versionNumber
				row.LatestVersionPricingModel = entity.PricingModel(*versionModel)
				row.LatestVersionStatus = *versionStatus
				row.LatestEffectiveFrom = *effectiveFrom
				row.LatestEffectiveTo = effectiveTo
				row.LatestChecksum = *checksum
			}
			detail = &row
		}

		if bracketID != nil {
			brackets = append(brackets, entity.PricingScheduleDetailBracket{
				ID:                       *bracketID,
				RangeStartQuantity:       *start,
				RangeEndQuantity:         end,
				PriceNumeratorMicroUnits: *numerator,
				PriceDenominatorQuantity: *denominator,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("pricing schedule repo: detail rows: %w", err)
	}
	if detail == nil {
		return nil, nil, billingTaxonomy.ErrPricingScheduleNotFound
	}

	return detail, brackets, nil
}

// UpdatePricingScheduleMetadata cập nhật display_name với Optimistic Concurrency Control (OCC) qua `metadata_version`.
func (r *pricingScheduleRepository) UpdatePricingScheduleMetadata(
	ctx context.Context,
	update entity.PricingScheduleMetadataCommand,
) (*entity.PricingScheduleMetadataUpdated, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: begin metadata: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var result entity.PricingScheduleMetadataUpdated

	// Khóa dòng schedule để kiểm tra OCC version
	if err := tx.QueryRow(ctx, `
		SELECT id, code, display_name, metadata_version, updated_at
		FROM billing.pricing_schedules
		WHERE code=$1 FOR UPDATE`, update.ScheduleCode,
	).Scan(&result.ID, &result.Code, &result.DisplayName, &result.MetadataVersion, &result.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	} else if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: lock metadata: %w", err)
	}

	if result.MetadataVersion != update.MetadataVersion {
		return nil, billingTaxonomy.ErrPricingScheduleMetadataConflict
	}

	// Cập nhật display_name và tăng monotonic metadata_version
	if err := tx.QueryRow(ctx, `
		UPDATE billing.pricing_schedules
		SET display_name=$1, metadata_version=metadata_version+1, updated_at=NOW()
		WHERE id=$2
		RETURNING display_name, metadata_version, updated_at`, update.DisplayName, result.ID,
	).Scan(&result.DisplayName, &result.MetadataVersion, &result.UpdatedAt); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: update metadata: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: commit metadata: %w", err)
	}

	return &result, nil
}
