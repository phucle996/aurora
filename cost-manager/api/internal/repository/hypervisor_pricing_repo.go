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

type hypervisorPricingRepository struct {
	db *pgxpool.Pool
}

func NewHypervisorPricingRepository(db *pgxpool.Pool) billingRepoInterface.HypervisorPricingRepository {
	return &hypervisorPricingRepository{db: db}
}

func (r *hypervisorPricingRepository) GetActiveHypervisorPricingSnapshot(ctx context.Context, chargeKind entity.ChargeKindCode, at time.Time) (*entity.HypervisorPricingSnapshot, error) {
	rows, err := r.db.Query(ctx, `
		WITH winner AS (
			SELECT s.id AS schedule_id,s.code,s.charge_kind_code,c.module_code,c.raw_input_unit,s.pricing_model,s.currency,
			       v.id AS version_id,v.version_number,v.effective_from,v.effective_to,v.checksum
			FROM billing.pricing_schedules s
			JOIN billing.charge_kind_catalog c ON c.code=s.charge_kind_code
			JOIN billing.pricing_schedule_versions v ON v.pricing_schedule_id=s.id
			WHERE s.charge_kind_code=$1 AND c.module_code='hypervisor' AND s.status='ACTIVE'
			  AND v.status <> 'CANCELLED' AND v.effective_from <= $2
			  AND (v.effective_to IS NULL OR $2 < v.effective_to)
			ORDER BY v.effective_from DESC,s.id LIMIT 1
		)
		SELECT w.schedule_id,w.code,w.charge_kind_code,w.module_code,w.raw_input_unit,w.pricing_model,w.currency,
		       w.version_id,w.version_number,w.effective_from,w.effective_to,w.checksum,b.id,b.range_start_quantity,
		       b.range_end_quantity,b.price_numerator_micro_units,b.price_denominator_quantity
		FROM winner w JOIN billing.pricing_schedule_scalar_brackets b ON b.pricing_schedule_version_id=w.version_id
		ORDER BY b.range_start_quantity`, string(chargeKind), at)
	if err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: active base snapshot: %w", err)
	}
	defer rows.Close()
	var snapshot *entity.HypervisorPricingSnapshot
	for rows.Next() {
		var scheduleID, versionID, bracketID uuid.UUID
		var code, kind, module, unit, model, currency, checksum string
		var version int
		var effectiveFrom time.Time
		var effectiveTo *time.Time
		var start, numerator, denominator int64
		var end *int64
		if err := rows.Scan(&scheduleID, &code, &kind, &module, &unit, &model, &currency, &versionID, &version, &effectiveFrom, &effectiveTo, &checksum, &bracketID, &start, &end, &numerator, &denominator); err != nil {
			return nil, fmt.Errorf("Hypervisor pricing repo: scan active base snapshot: %w", err)
		}
		if snapshot == nil {
			snapshot = &entity.HypervisorPricingSnapshot{PricingScheduleID: scheduleID, VersionID: versionID, Code: code, ChargeKindCode: entity.ChargeKindCode(kind), ModuleCode: module, RawInputUnit: unit, PricingModel: entity.PricingModel(model), Currency: currency, VersionNumber: version, EffectiveFrom: effectiveFrom, EffectiveTo: effectiveTo, Checksum: checksum}
		}
		snapshot.Brackets = append(snapshot.Brackets, entity.HypervisorPricingSnapshotBracket{ID: bracketID, RangeStartQuantity: start, RangeEndQuantity: end, PriceNumeratorMicroUnits: numerator, PriceDenominatorQuantity: denominator})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: iterate active base snapshot: %w", err)
	}
	if snapshot == nil {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	}
	return snapshot, nil
}

func (r *hypervisorPricingRepository) GetHypervisorBasePricePublishTarget(ctx context.Context, code string) (*entity.HypervisorBasePricePublishTarget, error) {
	var target entity.HypervisorBasePricePublishTarget
	var kind, model string
	err := r.db.QueryRow(ctx, `
		WITH target AS (
			SELECT s.id,s.code,s.charge_kind_code,s.pricing_model,s.currency FROM billing.pricing_schedules s
			JOIN billing.charge_kind_catalog k ON k.code=s.charge_kind_code
			WHERE s.code=$1 AND s.status='ACTIVE' AND k.module_code='hypervisor' AND s.charge_kind_code IN (
			'hypervisor.vcpu.allocated_second','hypervisor.memory_mib.allocated_second','hypervisor.disk_gib.allocated_second','hypervisor.network_in.byte','hypervisor.network_out.byte')
		) SELECT id,code,charge_kind_code,pricing_model,currency FROM target`, code).Scan(&target.PricingScheduleID, &target.ScheduleCode, &kind, &model, &target.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: base publish target: %w", err)
	}
	target.ChargeKindCode = entity.ChargeKindCode(kind)
	target.PricingModel = entity.PricingModel(model)
	return &target, nil
}

func (r *hypervisorPricingRepository) CreateHypervisorBasePriceVersion(ctx context.Context, create entity.HypervisorBasePricePublishCommand, brackets []entity.HypervisorBasePriceBracketCommand) (*entity.HypervisorBasePricePublished, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: begin base publish: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var scheduleID uuid.UUID
	var kind, model string
	err = tx.QueryRow(ctx, `WITH target AS (
		SELECT s.id,s.charge_kind_code,s.pricing_model::text FROM billing.pricing_schedules s JOIN billing.charge_kind_catalog k ON k.code=s.charge_kind_code
		WHERE s.code=$1 AND s.status='ACTIVE' AND k.module_code='hypervisor' AND s.charge_kind_code IN ('hypervisor.vcpu.allocated_second','hypervisor.memory_mib.allocated_second','hypervisor.disk_gib.allocated_second','hypervisor.network_in.byte','hypervisor.network_out.byte') FOR UPDATE OF s
	) SELECT id,charge_kind_code,pricing_model FROM target`, create.ScheduleCode).Scan(&scheduleID, &kind, &model)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPricingScheduleNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: lock base schedule: %w", err)
	}
	var latest int
	var latestEffective time.Time
	err = tx.QueryRow(ctx, `SELECT version_number,effective_from FROM billing.pricing_schedule_versions WHERE pricing_schedule_id=$1 AND status <> 'CANCELLED' ORDER BY version_number DESC LIMIT 1`, scheduleID).Scan(&latest, &latestEffective)
	if errors.Is(err, pgx.ErrNoRows) {
		latest = 0
	} else if err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: latest base version: %w", err)
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
		if _, err = tx.Exec(ctx, `UPDATE billing.pricing_schedule_versions SET effective_to=$1,status=CASE WHEN status='ACTIVE' THEN 'SUPERSEDED' ELSE status END WHERE pricing_schedule_id=$2 AND version_number=$3 AND effective_to IS NULL`, create.EffectiveFrom, scheduleID, latest); err != nil {
			return nil, fmt.Errorf("Hypervisor pricing repo: close prior base version: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO billing.pricing_schedule_versions (id,pricing_schedule_id,pricing_model,version_number,status,effective_from,checksum,change_reason,created_by) VALUES ($1,$2,$3::billing.pricing_model,$4,$5,$6,$7,$8,$9)`, versionID, scheduleID, model, latest+1, status, create.EffectiveFrom, create.Checksum, create.ChangeReason, create.CreatedBy); err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: insert base version: %w", err)
	}
	for _, bracket := range brackets {
		if _, err = tx.Exec(ctx, `INSERT INTO billing.pricing_schedule_scalar_brackets (id,pricing_schedule_version_id,range_start_quantity,range_end_quantity,price_numerator_micro_units,price_denominator_quantity) VALUES ($1,$2,$3,$4,$5,$6)`, uuid.New(), versionID, bracket.RangeStartQuantity, bracket.RangeEndQuantity, bracket.PriceNumeratorMicroUnits, bracket.PriceDenominatorQuantity); err != nil {
			return nil, fmt.Errorf("Hypervisor pricing repo: insert base bracket: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO billing.pricing_outbox (id,event_type,pricing_schedule_id,version_id,module_code,charge_kind_code,effective_from,checksum) VALUES ($1,'PRICING_SCHEDULE_VERSION_PUBLISHED',$2,$3,'hypervisor',$4,$5,$6)`, uuid.New(), scheduleID, versionID, kind, create.EffectiveFrom, create.Checksum); err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: insert base outbox: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: commit base publish: %w", err)
	}
	return &entity.HypervisorBasePricePublished{ID: versionID, PricingScheduleID: scheduleID, ChargeKindCode: entity.ChargeKindCode(kind), VersionNumber: latest + 1, PricingModel: entity.PricingModel(model), Status: status, EffectiveFrom: create.EffectiveFrom, Checksum: create.Checksum}, nil
}

func (r *hypervisorPricingRepository) GetActiveHypervisorZonePriceAdjustment(ctx context.Context, zoneID uuid.UUID, at time.Time) (*entity.HypervisorZoneAdjustmentSnapshot, error) {
	var adjustment entity.HypervisorZoneAdjustmentSnapshot
	err := r.db.QueryRow(ctx, `
		SELECT id,zone_id,version_number,effective_from,multiplier_numerator,multiplier_denominator,checksum
		FROM billing.hypervisor_zone_price_adjustment_versions
		WHERE zone_id=$1 AND status <> 'CANCELLED'
		  AND effective_from <= $2 AND (effective_to IS NULL OR $2 < effective_to)
		ORDER BY version_number DESC LIMIT 1`, zoneID, at).Scan(
		&adjustment.ID, &adjustment.ZoneID, &adjustment.VersionNumber,
		&adjustment.EffectiveFrom, &adjustment.MultiplierNumerator,
		&adjustment.MultiplierDenominator, &adjustment.Checksum,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: active Zone adjustment: %w", err)
	}
	return &adjustment, nil
}

func (r *hypervisorPricingRepository) CreateHypervisorZonePriceAdjustment(ctx context.Context, create entity.HypervisorZoneAdjustmentPublishCommand) (*entity.HypervisorZoneAdjustmentPublished, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: begin Zone adjustment: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "hypervisor-zone-price-adjustment:"+create.ZoneID.String()); err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: lock Zone adjustment: %w", err)
	}
	var latest int
	var latestEffective time.Time
	err = tx.QueryRow(ctx, `SELECT version_number,effective_from FROM billing.hypervisor_zone_price_adjustment_versions WHERE zone_id=$1 AND status <> 'CANCELLED' ORDER BY version_number DESC LIMIT 1 FOR UPDATE`, create.ZoneID).Scan(&latest, &latestEffective)
	if errors.Is(err, pgx.ErrNoRows) {
		latest = 0
	} else if err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: latest Zone adjustment: %w", err)
	}
	if latest != create.ExpectedLatestVersion || (latest > 0 && !create.EffectiveFrom.After(latestEffective)) {
		return nil, billingTaxonomy.ErrHypervisorZoneAdjustmentConflict
	}
	if latest > 0 {
		if _, err := tx.Exec(ctx, `UPDATE billing.hypervisor_zone_price_adjustment_versions SET effective_to=$1,status=CASE WHEN status='ACTIVE' THEN 'SUPERSEDED' ELSE status END WHERE zone_id=$2 AND version_number=$3 AND effective_to IS NULL`, create.EffectiveFrom, create.ZoneID, latest); err != nil {
			return nil, fmt.Errorf("Hypervisor pricing repo: close Zone adjustment: %w", err)
		}
	}
	id := uuid.New()
	status := "SCHEDULED"
	if !create.EffectiveFrom.After(time.Now().UTC()) {
		status = "ACTIVE"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO billing.hypervisor_zone_price_adjustment_versions (id,zone_id,version_number,status,effective_from,multiplier_numerator,multiplier_denominator,checksum,change_reason,created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, create.ZoneID, latest+1, status, create.EffectiveFrom, create.MultiplierNumerator, create.MultiplierDenominator, create.Checksum, create.ChangeReason, create.CreatedBy); err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: insert Zone adjustment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: commit Zone adjustment: %w", err)
	}
	return &entity.HypervisorZoneAdjustmentPublished{
		ID: id, ZoneID: create.ZoneID, VersionNumber: latest + 1, Status: status,
		EffectiveFrom: create.EffectiveFrom, MultiplierNumerator: create.MultiplierNumerator,
		MultiplierDenominator: create.MultiplierDenominator, Checksum: create.Checksum,
	}, nil
}

func (r *hypervisorPricingRepository) ListHypervisorZonePriceAdjustments(ctx context.Context, query entity.HypervisorZoneAdjustmentListQuery) ([]entity.HypervisorZoneAdjustmentListItem, bool, error) {
	rows, err := r.db.Query(ctx, `
		WITH history AS (
			SELECT id,zone_id,version_number,status,effective_from,effective_to,multiplier_numerator,multiplier_denominator,checksum,change_reason,created_by,created_at,
			       version_number=MAX(version_number) OVER () AS is_latest,
			       effective_from <= NOW() AND (effective_to IS NULL OR NOW() < effective_to) AS is_effective
			FROM billing.hypervisor_zone_price_adjustment_versions WHERE zone_id=$1
		), bounded AS (SELECT * FROM history ORDER BY version_number DESC LIMIT $2)
		SELECT id,zone_id,version_number,status,effective_from,effective_to,multiplier_numerator,multiplier_denominator,checksum,change_reason,created_by,created_at,is_latest,is_effective FROM bounded ORDER BY version_number DESC`, query.ZoneID, query.Limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("Hypervisor pricing repo: list Zone adjustments: %w", err)
	}
	defer rows.Close()
	items := make([]entity.HypervisorZoneAdjustmentListItem, 0, query.Limit+1)
	for rows.Next() {
		var item entity.HypervisorZoneAdjustmentListItem
		if err := rows.Scan(&item.ID, &item.ZoneID, &item.VersionNumber, &item.Status, &item.EffectiveFrom, &item.EffectiveTo, &item.MultiplierNumerator, &item.MultiplierDenominator, &item.Checksum, &item.ChangeReason, &item.CreatedBy, &item.CreatedAt, &item.IsLatest, &item.IsEffective); err != nil {
			return nil, false, fmt.Errorf("Hypervisor pricing repo: scan Zone adjustment: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("Hypervisor pricing repo: iterate Zone adjustments: %w", err)
	}
	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	return items, hasMore, nil
}

func (r *hypervisorPricingRepository) RefreshHypervisorPricingStatuses(ctx context.Context) error {
	if _, err := r.db.Exec(ctx, `WITH projected AS (
		SELECT v.id,CASE WHEN v.effective_to IS NOT NULL AND v.effective_to<=NOW() THEN 'SUPERSEDED' WHEN v.effective_from<=NOW() AND (v.effective_to IS NULL OR NOW()<v.effective_to) THEN 'ACTIVE' ELSE 'SCHEDULED' END desired_status
		FROM billing.pricing_schedule_versions v JOIN billing.pricing_schedules s ON s.id=v.pricing_schedule_id JOIN billing.charge_kind_catalog k ON k.code=s.charge_kind_code
		WHERE v.status<>'CANCELLED' AND k.module_code='hypervisor')
		UPDATE billing.pricing_schedule_versions v SET status=projected.desired_status FROM projected WHERE v.id=projected.id AND v.status IS DISTINCT FROM projected.desired_status`); err != nil {
		return fmt.Errorf("Hypervisor pricing repo: refresh base statuses: %w", err)
	}
	if _, err := r.db.Exec(ctx, `WITH projected AS (
		SELECT id,CASE WHEN effective_to IS NOT NULL AND effective_to<=NOW() THEN 'SUPERSEDED' WHEN effective_from<=NOW() AND (effective_to IS NULL OR NOW()<effective_to) THEN 'ACTIVE' ELSE 'SCHEDULED' END desired_status
		FROM billing.hypervisor_zone_price_adjustment_versions WHERE status<>'CANCELLED')
		UPDATE billing.hypervisor_zone_price_adjustment_versions v SET status=projected.desired_status FROM projected WHERE v.id=projected.id AND v.status IS DISTINCT FROM projected.desired_status`); err != nil {
		return fmt.Errorf("Hypervisor pricing repo: refresh Zone statuses: %w", err)
	}
	return nil
}

func (r *hypervisorPricingRepository) ClaimHypervisorPricingOutbox(ctx context.Context, claimToken uuid.UUID, leaseUntil time.Time, limit int) ([]*entity.PricingOutboxRow, error) {
	rows, err := r.db.Query(ctx, `WITH candidates AS (
		SELECT id FROM billing.pricing_outbox WHERE module_code='hypervisor' AND published_at IS NULL AND available_at<=NOW() AND (lease_until IS NULL OR lease_until<NOW()) ORDER BY occurred_at,id FOR UPDATE SKIP LOCKED LIMIT $3
	), claimed AS (
		UPDATE billing.pricing_outbox o SET claim_token=$1,lease_until=$2 FROM candidates c WHERE o.id=c.id
		RETURNING o.id,o.pricing_schedule_id,o.version_id,o.charge_kind_code,o.effective_from,o.checksum,o.occurred_at,o.claim_token,o.retry_count
	)
	SELECT c.id,c.pricing_schedule_id,c.version_id,v.version_number,c.charge_kind_code,c.effective_from,c.checksum,c.occurred_at,c.claim_token,c.retry_count FROM claimed c JOIN billing.pricing_schedule_versions v ON v.id=c.version_id ORDER BY c.occurred_at,c.id`, claimToken, leaseUntil, limit)
	if err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: claim outbox: %w", err)
	}
	defer rows.Close()
	batch := make([]*entity.PricingOutboxRow, 0, limit)
	for rows.Next() {
		var row entity.PricingOutboxRow
		row.ModuleCode = "hypervisor"
		if err := rows.Scan(&row.ID, &row.PricingScheduleID, &row.VersionID, &row.VersionNumber, &row.ChargeKindCode, &row.EffectiveFrom, &row.Checksum, &row.OccurredAt, &row.ClaimToken, &row.RetryCount); err != nil {
			return nil, fmt.Errorf("Hypervisor pricing repo: scan claimed outbox: %w", err)
		}
		batch = append(batch, &row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Hypervisor pricing repo: iterate claimed outbox: %w", err)
	}
	return batch, nil
}

func (r *hypervisorPricingRepository) MarkHypervisorPricingOutboxPublished(ctx context.Context, id, claimToken uuid.UUID) error {
	result, err := r.db.Exec(ctx, `UPDATE billing.pricing_outbox SET published_at=NOW(),claim_token=NULL,lease_until=NULL,last_error=NULL WHERE id=$1 AND claim_token=$2 AND published_at IS NULL`, id, claimToken)
	if err != nil {
		return fmt.Errorf("Hypervisor pricing repo: mark outbox published: %w", err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("Hypervisor pricing repo: outbox claim lost for %s", id)
	}
	return nil
}

func (r *hypervisorPricingRepository) RetryHypervisorPricingOutbox(ctx context.Context, id, claimToken uuid.UUID, lastError string, availableAt time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE billing.pricing_outbox SET retry_count=retry_count+1,last_error=$3,available_at=$4,claim_token=NULL,lease_until=NULL WHERE id=$1 AND claim_token=$2 AND published_at IS NULL`, id, claimToken, lastError, availableAt)
	if err != nil {
		return fmt.Errorf("Hypervisor pricing repo: retry outbox: %w", err)
	}
	return nil
}
