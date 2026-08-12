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

type pricingScheduleRepository struct {
	db *pgxpool.Pool
}

func NewPricingScheduleRepository(db *pgxpool.Pool) billingRepoInterface.PricingScheduleRepository {
	return &pricingScheduleRepository{db: db}
}

func (r *pricingScheduleRepository) ListPricingSchedules(ctx context.Context, page, limit int, chargeKind entity.ChargeKindCode, search string) ([]*entity.PricingSchedule, int64, error) {
	offset := (page - 1) * limit
	pattern := ""
	if search != "" {
		pattern = "%" + search + "%"
	}
	var total int64
	if err := r.db.QueryRow(ctx, `SELECT count(*) FROM billing.pricing_schedules WHERE ($1='' OR charge_kind_code=$1) AND ($2='' OR code ILIKE $3 OR display_name ILIKE $3)`, string(chargeKind), search, pattern).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("pricing schedule repo: count: %w", err)
	}
	rows, err := r.db.Query(ctx, `
		SELECT id, code, display_name, charge_kind_code, pricing_model, scope_type, zone_id,
		       currency, metadata_version, status, created_at, updated_at
		FROM billing.pricing_schedules
		WHERE ($1='' OR charge_kind_code=$1) AND ($2='' OR code ILIKE $3 OR display_name ILIKE $3)
		ORDER BY created_at DESC, code ASC LIMIT $4 OFFSET $5`, string(chargeKind), search, pattern, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("pricing schedule repo: list: %w", err)
	}
	defer rows.Close()
	result := make([]*entity.PricingSchedule, 0)
	for rows.Next() {
		var schedule entity.PricingSchedule
		var chargeKind, model, scope string
		if err := rows.Scan(&schedule.ID, &schedule.Code, &schedule.DisplayName, &chargeKind, &model, &scope, &schedule.ZoneID, &schedule.Currency, &schedule.MetadataVersion, &schedule.Status, &schedule.CreatedAt, &schedule.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("pricing schedule repo: scan: %w", err)
		}
		schedule.ChargeKindCode = entity.ChargeKindCode(chargeKind)
		schedule.PricingModel = entity.PricingModel(model)
		schedule.ScopeType = entity.PricingScope(scope)
		result = append(result, &schedule)
	}
	return result, total, rows.Err()
}

func (r *pricingScheduleRepository) GetPricingScheduleDetail(ctx context.Context, code string) (*entity.PricingScheduleDetail, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.id, s.code, s.display_name, s.charge_kind_code, s.pricing_model, s.scope_type, s.zone_id,
		       s.currency, s.metadata_version, s.status, s.created_at, s.updated_at,
		       v.id, v.version_number, v.pricing_model, v.status, v.effective_from, v.effective_to, v.checksum,
		       b.id, b.range_start_quantity, b.range_end_quantity, b.price_numerator_micro_units, b.price_denominator_quantity
		FROM billing.pricing_schedules s
		JOIN LATERAL (SELECT * FROM billing.pricing_schedule_versions candidate WHERE candidate.pricing_schedule_id=s.id ORDER BY version_number DESC LIMIT 1) v ON TRUE
		LEFT JOIN billing.pricing_schedule_scalar_brackets b ON b.pricing_schedule_version_id=v.id
		WHERE s.code=$1 ORDER BY b.range_start_quantity`, code)
	if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: detail: %w", err)
	}
	defer rows.Close()
	var detail *entity.PricingScheduleDetail
	for rows.Next() {
		var schedule entity.PricingSchedule
		var chargeKind, model, scope, versionModel, versionStatus string
		var versionID uuid.UUID
		var versionNumber int
		var effectiveFrom time.Time
		var effectiveTo *time.Time
		var checksum string
		var bracketID *uuid.UUID
		var start int64
		var end *int64
		var numerator, denominator int64
		if err := rows.Scan(&schedule.ID, &schedule.Code, &schedule.DisplayName, &chargeKind, &model, &scope, &schedule.ZoneID, &schedule.Currency, &schedule.MetadataVersion, &schedule.Status, &schedule.CreatedAt, &schedule.UpdatedAt, &versionID, &versionNumber, &versionModel, &versionStatus, &effectiveFrom, &effectiveTo, &checksum, &bracketID, &start, &end, &numerator, &denominator); err != nil {
			return nil, fmt.Errorf("pricing schedule repo: detail scan: %w", err)
		}
		schedule.ChargeKindCode = entity.ChargeKindCode(chargeKind)
		schedule.PricingModel = entity.PricingModel(model)
		schedule.ScopeType = entity.PricingScope(scope)
		if detail == nil {
			detail = &entity.PricingScheduleDetail{Schedule: schedule, LatestVersion: entity.PricingScheduleVersion{ID: versionID, PricingScheduleID: schedule.ID, VersionNumber: versionNumber, PricingModel: entity.PricingModel(versionModel), Status: versionStatus, EffectiveFrom: effectiveFrom, EffectiveTo: effectiveTo, Checksum: checksum}}
		}
		if bracketID != nil {
			detail.LatestVersion.Brackets = append(detail.LatestVersion.Brackets, entity.ScalarBracketInput{ID: *bracketID, RangeStartQuantity: start, RangeEndQuantity: end, PriceNumeratorMicroUnits: numerator, PriceDenominatorQuantity: denominator})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: detail rows: %w", err)
	}
	if detail == nil {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	}
	return detail, nil
}

func (r *pricingScheduleRepository) GetActivePricingSnapshot(ctx context.Context, chargeKind entity.ChargeKindCode, zoneID *uuid.UUID, at time.Time) (*entity.PricingSnapshot, error) {
	rows, err := r.db.Query(ctx, `
		WITH winner AS (
			SELECT s.id, s.code, s.charge_kind_code, c.module_code, s.pricing_model, s.scope_type, s.zone_id, s.currency,
			       v.id AS version_id, v.version_number, v.effective_from, v.effective_to, v.checksum
			FROM billing.pricing_schedules s
			JOIN billing.charge_kind_catalog c ON c.code=s.charge_kind_code
			JOIN billing.pricing_schedule_versions v ON v.pricing_schedule_id=s.id
			WHERE s.charge_kind_code=$1 AND s.status='ACTIVE' AND v.status <> 'CANCELLED'
			  AND v.effective_from <= $2 AND (v.effective_to IS NULL OR $2 < v.effective_to)
			  AND ((s.scope_type='ZONE' AND s.zone_id=$3) OR s.scope_type='GLOBAL')
			ORDER BY CASE WHEN s.scope_type='ZONE' THEN 0 ELSE 1 END, v.effective_from DESC, s.id
			LIMIT 1
		)
		SELECT w.id, w.code, w.charge_kind_code, w.module_code, w.pricing_model, w.scope_type, w.zone_id, w.currency,
		       w.version_id, w.version_number, w.effective_from, w.effective_to, w.checksum,
		       b.id, b.range_start_quantity, b.range_end_quantity, b.price_numerator_micro_units, b.price_denominator_quantity
		FROM winner w JOIN billing.pricing_schedule_scalar_brackets b ON b.pricing_schedule_version_id=w.version_id
		ORDER BY b.range_start_quantity`, string(chargeKind), at, nullableUUID(zoneID))
	if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: active snapshot: %w", err)
	}
	defer rows.Close()
	var snapshot *entity.PricingSnapshot
	for rows.Next() {
		var s entity.PricingSnapshot
		var chargeKindRaw, model, scope string
		var versionID, scheduleID, bracketID uuid.UUID
		var zone *uuid.UUID
		var versionNumber int
		var effectiveFrom time.Time
		var effectiveTo *time.Time
		var checksum, currency, module string
		var start int64
		var end *int64
		var numerator, denominator int64
		if err := rows.Scan(&scheduleID, &s.Code, &chargeKindRaw, &module, &model, &scope, &zone, &currency, &versionID, &versionNumber, &effectiveFrom, &effectiveTo, &checksum, &bracketID, &start, &end, &numerator, &denominator); err != nil {
			return nil, fmt.Errorf("pricing schedule repo: active scan: %w", err)
		}
		if snapshot == nil {
			snapshot = &entity.PricingSnapshot{PricingScheduleID: scheduleID, VersionID: versionID, Code: s.Code, ChargeKindCode: entity.ChargeKindCode(chargeKindRaw), ModuleCode: module, PricingModel: entity.PricingModel(model), ScopeType: entity.PricingScope(scope), ZoneID: zone, RawInputUnit: rawInputUnit(entity.ChargeKindCode(chargeKindRaw)), VersionNumber: versionNumber, EffectiveFrom: effectiveFrom, EffectiveTo: effectiveTo, Checksum: checksum, Currency: currency}
		}
		snapshot.Brackets = append(snapshot.Brackets, entity.ScalarBracketInput{ID: bracketID, RangeStartQuantity: start, RangeEndQuantity: end, PriceNumeratorMicroUnits: numerator, PriceDenominatorQuantity: denominator})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: active rows: %w", err)
	}
	if snapshot == nil {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	}
	return snapshot, nil
}

func nullableUUID(value *uuid.UUID) any {
	if value == nil || *value == uuid.Nil {
		return nil
	}
	return *value
}

func rawInputUnit(kind entity.ChargeKindCode) string {
	switch kind {
	case entity.ChargeKindStorageCapacity:
		return "GB_HOUR_MICRO"
	case entity.ChargeKindStorageNetworkIn, entity.ChargeKindStorageNetworkOut:
		return "BYTE"
	default:
		return ""
	}
}

func (r *pricingScheduleRepository) UpdatePricingScheduleMetadata(ctx context.Context, update entity.PricingScheduleMetadataUpdate) (*entity.PricingSchedule, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: begin metadata: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var schedule entity.PricingSchedule
	var model, scope, chargeKind string
	if err := tx.QueryRow(ctx, `SELECT id, code, display_name, charge_kind_code, pricing_model, scope_type, zone_id, currency, metadata_version, status, created_at, updated_at FROM billing.pricing_schedules WHERE code=$1 FOR UPDATE`, update.ScheduleCode).Scan(&schedule.ID, &schedule.Code, &schedule.DisplayName, &chargeKind, &model, &scope, &schedule.ZoneID, &schedule.Currency, &schedule.MetadataVersion, &schedule.Status, &schedule.CreatedAt, &schedule.UpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	} else if err != nil {
		return nil, fmt.Errorf("pricing schedule repo: lock metadata: %w", err)
	}
	if schedule.MetadataVersion != update.MetadataVersion {
		return nil, billingTaxonomy.ErrPricingScheduleMetadataConflict
	}
	if err := tx.QueryRow(ctx, `UPDATE billing.pricing_schedules SET display_name=$1, metadata_version=metadata_version+1, updated_at=NOW() WHERE id=$2 RETURNING display_name, metadata_version, updated_at`, update.DisplayName, schedule.ID).Scan(&schedule.DisplayName, &schedule.MetadataVersion, &schedule.UpdatedAt); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: update metadata: %w", err)
	}
	schedule.ChargeKindCode = entity.ChargeKindCode(chargeKind)
	schedule.PricingModel = entity.PricingModel(model)
	schedule.ScopeType = entity.PricingScope(scope)
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: commit metadata: %w", err)
	}
	return &schedule, nil
}

func (r *pricingScheduleRepository) CreatePricingScheduleVersion(ctx context.Context, create entity.PricingScheduleVersionCreate) (*entity.PricingScheduleVersion, error) {
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
	for _, bracket := range create.Brackets {
		if _, err := tx.Exec(ctx, `INSERT INTO billing.pricing_schedule_scalar_brackets (id, pricing_schedule_version_id, range_start_quantity, range_end_quantity, price_numerator_micro_units, price_denominator_quantity) VALUES ($1,$2,$3,$4,$5,$6)`, uuid.New(), versionID, bracket.RangeStartQuantity, bracket.RangeEndQuantity, bracket.PriceNumeratorMicroUnits, bracket.PriceDenominatorQuantity); err != nil {
			return nil, fmt.Errorf("pricing schedule repo: insert bracket: %w", err)
		}
	}
	var chargeKind string
	var scope entity.PricingScope
	var zoneID *uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT charge_kind_code, scope_type, zone_id FROM billing.pricing_schedules WHERE id=$1`, scheduleID).Scan(&chargeKind, &scope, &zoneID); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: read schedule lineage: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO billing.pricing_outbox (id, event_type, pricing_schedule_id, version_id, module_code, charge_kind_code, scope_type, zone_id, effective_from, checksum) SELECT $1, 'PRICING_SCHEDULE_VERSION_PUBLISHED', s.id, $2, c.module_code, s.charge_kind_code, s.scope_type, s.zone_id, $3, $4 FROM billing.pricing_schedules s JOIN billing.charge_kind_catalog c ON c.code=s.charge_kind_code WHERE s.id=$5`, uuid.New(), versionID, create.EffectiveFrom, create.Checksum, scheduleID); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: insert outbox: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pricing schedule repo: commit version: %w", err)
	}
	return &entity.PricingScheduleVersion{ID: versionID, PricingScheduleID: scheduleID, VersionNumber: latest + 1, PricingModel: entity.PricingModel(model), Status: status, EffectiveFrom: create.EffectiveFrom, Checksum: create.Checksum, Brackets: create.Brackets}, nil
}
