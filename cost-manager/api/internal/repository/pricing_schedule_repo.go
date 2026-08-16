package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type pricingScheduleRepository struct {
	db *pgxpool.Pool
}

func NewPricingScheduleRepository(db *pgxpool.Pool) *pricingScheduleRepository {
	return &pricingScheduleRepository{db: db}
}

func (r *pricingScheduleRepository) ListPricingSchedules(ctx context.Context, page, limit int, chargeKind entity.ChargeKindCode, search string) ([]*entity.PricingScheduleListItem, int64, error) {
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
		FROM filtered ORDER BY created_at DESC, code ASC LIMIT $4 OFFSET $5`, string(chargeKind), search, pattern, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("pricing schedule repo: list: %w", err)
	}
	defer rows.Close()
	result := make([]*entity.PricingScheduleListItem, 0)
	var total int64
	for rows.Next() {
		var schedule entity.PricingScheduleListItem
		var chargeKind, model string
		if err := rows.Scan(&schedule.ID, &schedule.Code, &schedule.DisplayName, &chargeKind, &model, &schedule.Currency, &schedule.MetadataVersion, &schedule.Status, &schedule.CreatedAt, &schedule.UpdatedAt, &total); err != nil {
			return nil, 0, fmt.Errorf("pricing schedule repo: scan: %w", err)
		}
		schedule.ChargeKindCode = entity.ChargeKindCode(chargeKind)
		schedule.PricingModel = entity.PricingModel(model)
		result = append(result, &schedule)
	}
	if len(result) == 0 && page > 1 {
		if err := r.db.QueryRow(ctx, `WITH filtered AS (SELECT 1 FROM billing.pricing_schedules WHERE ($1='' OR charge_kind_code=$1) AND ($2='' OR code ILIKE $3 OR display_name ILIKE $3)) SELECT COUNT(*) FROM filtered`, string(chargeKind), search, pattern).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("pricing schedule repo: count empty page: %w", err)
		}
	}
	return result, total, rows.Err()
}

func (r *pricingScheduleRepository) GetPricingScheduleDetail(ctx context.Context, code string) (*entity.PricingScheduleDetail, []entity.PricingScheduleDetailBracket, error) {
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
		FROM target t LEFT JOIN latest v ON TRUE
		LEFT JOIN billing.pricing_schedule_scalar_brackets b ON b.pricing_schedule_version_id=v.id
		ORDER BY b.range_start_quantity`, code)
	if err != nil {
		return nil, nil, fmt.Errorf("pricing schedule repo: detail: %w", err)
	}
	defer rows.Close()
	var detail *entity.PricingScheduleDetail
	var brackets []entity.PricingScheduleDetailBracket
	for rows.Next() {
		var chargeKind, model string
		var versionModel, versionStatus, checksum *string
		var versionID *uuid.UUID
		var versionNumber *int
		var effectiveFrom *time.Time
		var effectiveTo *time.Time
		var bracketID *uuid.UUID
		var start *int64
		var end *int64
		var numerator, denominator *int64
		var row entity.PricingScheduleDetail
		if err := rows.Scan(&row.ID, &row.Code, &row.DisplayName, &chargeKind, &model, &row.Currency, &row.MetadataVersion, &row.Status, &row.CreatedAt, &row.UpdatedAt, &versionID, &versionNumber, &versionModel, &versionStatus, &effectiveFrom, &effectiveTo, &checksum, &bracketID, &start, &end, &numerator, &denominator); err != nil {
			return nil, nil, fmt.Errorf("pricing schedule repo: detail scan: %w", err)
		}
		if detail == nil {
			row.ChargeKindCode = entity.ChargeKindCode(chargeKind)
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
			brackets = append(brackets, entity.PricingScheduleDetailBracket{ID: *bracketID, RangeStartQuantity: *start, RangeEndQuantity: end, PriceNumeratorMicroUnits: *numerator, PriceDenominatorQuantity: *denominator})
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

func (r *pricingScheduleRepository) GetActivePricingSnapshot(ctx context.Context, chargeKind entity.ChargeKindCode, at time.Time) (*entity.PricingSnapshot, error) {
	rows, err := r.db.Query(ctx, `
		WITH winner AS (
			SELECT s.id, s.code, s.charge_kind_code, c.module_code, c.raw_input_unit, s.pricing_model, s.currency,
			       v.id AS version_id, v.version_number, v.effective_from, v.effective_to, v.checksum
			FROM billing.pricing_schedules s
			JOIN billing.charge_kind_catalog c ON c.code=s.charge_kind_code
			JOIN billing.pricing_schedule_versions v ON v.pricing_schedule_id=s.id
			WHERE s.charge_kind_code=$1 AND s.status='ACTIVE' AND v.status <> 'CANCELLED'
			  AND v.effective_from <= $2 AND (v.effective_to IS NULL OR $2 < v.effective_to)
			ORDER BY v.effective_from DESC, s.id
			LIMIT 1
		)
		SELECT w.id, w.code, w.charge_kind_code, w.module_code, w.raw_input_unit, w.pricing_model, w.currency,
		       w.version_id, w.version_number, w.effective_from, w.effective_to, w.checksum,
		       b.id, b.range_start_quantity, b.range_end_quantity, b.price_numerator_micro_units, b.price_denominator_quantity
		FROM winner w JOIN billing.pricing_schedule_scalar_brackets b ON b.pricing_schedule_version_id=w.version_id
		ORDER BY b.range_start_quantity`, string(chargeKind), at)
	if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: active snapshot: %w", err)
	}
	defer rows.Close()
	var snapshot *entity.PricingSnapshot
	for rows.Next() {
		var s entity.PricingSnapshot
		var chargeKindRaw, model, rawUnit string
		var versionID, scheduleID, bracketID uuid.UUID
		var versionNumber int
		var effectiveFrom time.Time
		var effectiveTo *time.Time
		var checksum, currency, module string
		var start int64
		var end *int64
		var numerator, denominator int64
		if err := rows.Scan(&scheduleID, &s.Code, &chargeKindRaw, &module, &rawUnit, &model, &currency, &versionID, &versionNumber, &effectiveFrom, &effectiveTo, &checksum, &bracketID, &start, &end, &numerator, &denominator); err != nil {
			return nil, fmt.Errorf("pricing schedule repo: active scan: %w", err)
		}
		if snapshot == nil {
			snapshot = &entity.PricingSnapshot{PricingScheduleID: scheduleID, VersionID: versionID, Code: s.Code, ChargeKindCode: entity.ChargeKindCode(chargeKindRaw), ModuleCode: module, PricingModel: entity.PricingModel(model), RawInputUnit: rawUnit, VersionNumber: versionNumber, EffectiveFrom: effectiveFrom, EffectiveTo: effectiveTo, Checksum: checksum, Currency: currency}
		}
		snapshot.Brackets = append(snapshot.Brackets, entity.PricingSnapshotBracket{ID: bracketID, RangeStartQuantity: start, RangeEndQuantity: end, PriceNumeratorMicroUnits: numerator, PriceDenominatorQuantity: denominator})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: active rows: %w", err)
	}
	if snapshot == nil {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	}
	return snapshot, nil
}

func (r *pricingScheduleRepository) UpdatePricingScheduleMetadata(ctx context.Context, update entity.PricingScheduleMetadataCommand) (*entity.PricingScheduleMetadataUpdated, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: begin metadata: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var result entity.PricingScheduleMetadataUpdated
	if err := tx.QueryRow(ctx, `SELECT id, code, display_name, metadata_version, updated_at FROM billing.pricing_schedules WHERE code=$1 FOR UPDATE`, update.ScheduleCode).Scan(&result.ID, &result.Code, &result.DisplayName, &result.MetadataVersion, &result.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	} else if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: lock metadata: %w", err)
	}
	if result.MetadataVersion != update.MetadataVersion {
		return nil, billingTaxonomy.ErrPricingScheduleMetadataConflict
	}
	if err := tx.QueryRow(ctx, `UPDATE billing.pricing_schedules SET display_name=$1, metadata_version=metadata_version+1, updated_at=NOW() WHERE id=$2 RETURNING display_name, metadata_version, updated_at`, update.DisplayName, result.ID).Scan(&result.DisplayName, &result.MetadataVersion, &result.UpdatedAt); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: update metadata: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: commit metadata: %w", err)
	}
	return &result, nil
}

func (r *pricingScheduleRepository) GetPricingScheduleVersionPublishTarget(ctx context.Context, code string) (*entity.PricingScheduleVersionPublishTarget, error) {
	var target entity.PricingScheduleVersionPublishTarget
	var chargeKind, model string
	err := r.db.QueryRow(ctx, `
		WITH target AS (
			SELECT id, code, charge_kind_code, pricing_model, currency
			FROM billing.pricing_schedules WHERE code=$1 AND status='ACTIVE'
		)
		SELECT id, code, charge_kind_code, pricing_model, currency FROM target`, code).Scan(
		&target.PricingScheduleID, &target.ScheduleCode, &chargeKind, &model, &target.Currency,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: publish target: %w", err)
	}
	target.ChargeKindCode = entity.ChargeKindCode(chargeKind)
	target.PricingModel = entity.PricingModel(model)
	return &target, nil
}

func (r *pricingScheduleRepository) CreatePricingScheduleVersion(ctx context.Context, create entity.PricingScheduleVersionPublishCommand, brackets []entity.PricingScheduleVersionPublishBracket) (*entity.PricingScheduleVersionPublished, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: begin version: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var scheduleID uuid.UUID
	var model string
	if err := tx.QueryRow(ctx, `SELECT id, pricing_model::text FROM billing.pricing_schedules WHERE code=$1 AND status='ACTIVE' FOR UPDATE`, create.ScheduleCode).Scan(&scheduleID, &model); errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	} else if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: lock schedule: %w", err)
	}
	var latest int
	var latestEffective time.Time
	if err := tx.QueryRow(ctx, `SELECT version_number, effective_from FROM billing.pricing_schedule_versions WHERE pricing_schedule_id=$1 AND status <> 'CANCELLED' ORDER BY version_number DESC LIMIT 1`, scheduleID).Scan(&latest, &latestEffective); errors.Is(err, pgx.ErrNoRows) {
		latest = 0
	} else if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: latest version: %w", err)
	}
	if latest != create.ExpectedLatestVersion {
		return nil, billingTaxonomy.ErrPricingScheduleVersionConflict
	}
	if latest > 0 && !create.EffectiveFrom.After(latestEffective) {
		return nil, billingTaxonomy.ErrPricingScheduleEffectiveConflict
	}
	versionID := uuid.New()
	status := "SCHEDULED"
	if !create.EffectiveFrom.After(time.Now().UTC()) {
		status = "ACTIVE"
	}
	if latest > 0 {
		if _, err := tx.Exec(ctx, `UPDATE billing.pricing_schedule_versions SET effective_to=$1, status=CASE WHEN status='ACTIVE' THEN 'SUPERSEDED' ELSE status END WHERE pricing_schedule_id=$2 AND version_number=$3 AND effective_to IS NULL`, create.EffectiveFrom, scheduleID, latest); err != nil {
			return nil, fmt.Errorf("pricing schedule repo: close old version: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO billing.pricing_schedule_versions (id, pricing_schedule_id, pricing_model, version_number, status, effective_from, checksum, change_reason, created_by) VALUES ($1,$2,$3::billing.pricing_model,$4,$5,$6,$7,$8,$9)`, versionID, scheduleID, model, latest+1, status, create.EffectiveFrom, create.Checksum, create.ChangeReason, create.CreatedBy); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: insert version: %w", err)
	}
	for _, bracket := range brackets {
		if _, err := tx.Exec(ctx, `INSERT INTO billing.pricing_schedule_scalar_brackets (id, pricing_schedule_version_id, range_start_quantity, range_end_quantity, price_numerator_micro_units, price_denominator_quantity) VALUES ($1,$2,$3,$4,$5,$6)`, uuid.New(), versionID, bracket.RangeStartQuantity, bracket.RangeEndQuantity, bracket.PriceNumeratorMicroUnits, bracket.PriceDenominatorQuantity); err != nil {
			return nil, fmt.Errorf("pricing schedule repo: insert bracket: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO billing.pricing_outbox (id, event_type, pricing_schedule_id, version_id, module_code, charge_kind_code, effective_from, checksum) SELECT $1, 'PRICING_SCHEDULE_VERSION_PUBLISHED', s.id, $2, c.module_code, s.charge_kind_code, $3, $4 FROM billing.pricing_schedules s JOIN billing.charge_kind_catalog c ON c.code=s.charge_kind_code WHERE s.id=$5`, uuid.New(), versionID, create.EffectiveFrom, create.Checksum, scheduleID); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: insert outbox: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: commit version: %w", err)
	}
	return &entity.PricingScheduleVersionPublished{ID: versionID, PricingScheduleID: scheduleID, VersionNumber: latest + 1, PricingModel: entity.PricingModel(model), Status: status, EffectiveFrom: create.EffectiveFrom, Checksum: create.Checksum}, nil
}

func (r *pricingScheduleRepository) GetActiveStorageZonePriceAdjustment(ctx context.Context, zoneID uuid.UUID, at time.Time) (*entity.StorageZoneAdjustmentSnapshot, error) {
	var adjustment entity.StorageZoneAdjustmentSnapshot
	err := r.db.QueryRow(ctx, `
		WITH effective AS (
			SELECT id, zone_id, version_number, effective_from,
			       multiplier_numerator, multiplier_denominator, checksum
		FROM billing.storage_zone_price_adjustment_versions
		WHERE zone_id=$1 AND status <> 'CANCELLED'
		  AND effective_from <= $2 AND (effective_to IS NULL OR $2 < effective_to)
		ORDER BY version_number DESC LIMIT 1
		)
		SELECT id, zone_id, version_number, effective_from, multiplier_numerator,
		       multiplier_denominator, checksum FROM effective`, zoneID, at).Scan(
		&adjustment.ID, &adjustment.ZoneID, &adjustment.VersionNumber,
		&adjustment.EffectiveFrom, &adjustment.MultiplierNumerator,
		&adjustment.MultiplierDenominator, &adjustment.Checksum,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: active Storage Zone adjustment: %w", err)
	}
	return &adjustment, nil
}

func (r *pricingScheduleRepository) CreateStorageZonePriceAdjustment(ctx context.Context, create entity.StorageZoneAdjustmentPublishCommand) (*entity.StorageZoneAdjustmentPublished, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: begin Storage Zone adjustment: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "storage-zone-price-adjustment:"+create.ZoneID.String()); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: lock Storage Zone adjustment: %w", err)
	}
	var latest int
	var latestEffective time.Time
	err = tx.QueryRow(ctx, `SELECT version_number, effective_from FROM billing.storage_zone_price_adjustment_versions WHERE zone_id=$1 AND status <> 'CANCELLED' ORDER BY version_number DESC LIMIT 1 FOR UPDATE`, create.ZoneID).Scan(&latest, &latestEffective)
	if errors.Is(err, pgx.ErrNoRows) {
		latest = 0
	} else if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: latest Storage Zone adjustment: %w", err)
	}
	if latest != create.ExpectedLatestVersion || (latest > 0 && !create.EffectiveFrom.After(latestEffective)) {
		return nil, billingTaxonomy.ErrStorageZoneAdjustmentConflict
	}
	if latest > 0 {
		if _, err := tx.Exec(ctx, `UPDATE billing.storage_zone_price_adjustment_versions SET effective_to=$1, status=CASE WHEN status='ACTIVE' THEN 'SUPERSEDED' ELSE status END WHERE zone_id=$2 AND version_number=$3 AND effective_to IS NULL`, create.EffectiveFrom, create.ZoneID, latest); err != nil {
			return nil, fmt.Errorf("pricing schedule repo: close Storage Zone adjustment: %w", err)
		}
	}
	id := uuid.New()
	status := "SCHEDULED"
	if !create.EffectiveFrom.After(time.Now().UTC()) {
		status = "ACTIVE"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO billing.storage_zone_price_adjustment_versions (id, zone_id, version_number, status, effective_from, multiplier_numerator, multiplier_denominator, checksum, change_reason, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, create.ZoneID, latest+1, status, create.EffectiveFrom, create.MultiplierNumerator, create.MultiplierDenominator, create.Checksum, create.ChangeReason, create.CreatedBy); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: insert Storage Zone adjustment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: commit Storage Zone adjustment: %w", err)
	}
	return &entity.StorageZoneAdjustmentPublished{ID: id, ZoneID: create.ZoneID, VersionNumber: latest + 1, Status: status, EffectiveFrom: create.EffectiveFrom, MultiplierNumerator: create.MultiplierNumerator, MultiplierDenominator: create.MultiplierDenominator, Checksum: create.Checksum}, nil
}
