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

type mailPricingRepository struct{ db *pgxpool.Pool }

func NewMailPricingRepository(db *pgxpool.Pool) *mailPricingRepository {
	return &mailPricingRepository{db: db}
}

func (r *mailPricingRepository) GetActiveMailZonePriceAdjustment(ctx context.Context, zoneID uuid.UUID, at time.Time) (*entity.MailZoneAdjustmentSnapshot, error) {
	var adjustment entity.MailZoneAdjustmentSnapshot
	err := r.db.QueryRow(ctx, `
		SELECT id,zone_id,version_number,effective_from,multiplier_numerator,multiplier_denominator,checksum
		FROM billing.mail_zone_price_adjustment_versions
		WHERE zone_id=$1 AND status <> 'CANCELLED'
		  AND effective_from <= $2 AND (effective_to IS NULL OR $2 < effective_to)
		ORDER BY version_number DESC LIMIT 1`, zoneID, at).Scan(
		&adjustment.ID, &adjustment.ZoneID, &adjustment.VersionNumber, &adjustment.EffectiveFrom,
		&adjustment.MultiplierNumerator, &adjustment.MultiplierDenominator, &adjustment.Checksum,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("Mail pricing repo: active Zone adjustment: %w", err)
	}
	return &adjustment, nil
}

func (r *mailPricingRepository) CreateMailZonePriceAdjustment(ctx context.Context, create entity.MailZoneAdjustmentPublishCommand) (*entity.MailZoneAdjustmentPublished, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("Mail pricing repo: begin Zone adjustment: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "mail-zone-price-adjustment:"+create.ZoneID.String()); err != nil {
		return nil, fmt.Errorf("Mail pricing repo: lock Zone adjustment: %w", err)
	}
	var latest int
	var latestEffective time.Time
	err = tx.QueryRow(ctx, `SELECT version_number,effective_from FROM billing.mail_zone_price_adjustment_versions WHERE zone_id=$1 AND status <> 'CANCELLED' ORDER BY version_number DESC LIMIT 1 FOR UPDATE`, create.ZoneID).Scan(&latest, &latestEffective)
	if errors.Is(err, pgx.ErrNoRows) {
		latest = 0
	} else if err != nil {
		return nil, fmt.Errorf("Mail pricing repo: latest Zone adjustment: %w", err)
	}
	if latest != create.ExpectedLatestVersion || (latest > 0 && !create.EffectiveFrom.After(latestEffective)) {
		return nil, billingTaxonomy.ErrMailZoneAdjustmentConflict
	}
	if latest > 0 {
		if _, err := tx.Exec(ctx, `UPDATE billing.mail_zone_price_adjustment_versions SET effective_to=$1,status=CASE WHEN status='ACTIVE' THEN 'SUPERSEDED' ELSE status END WHERE zone_id=$2 AND version_number=$3 AND effective_to IS NULL`, create.EffectiveFrom, create.ZoneID, latest); err != nil {
			return nil, fmt.Errorf("Mail pricing repo: close Zone adjustment: %w", err)
		}
	}
	id := uuid.New()
	status := "SCHEDULED"
	if !create.EffectiveFrom.After(time.Now().UTC()) {
		status = "ACTIVE"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO billing.mail_zone_price_adjustment_versions (id,zone_id,version_number,status,effective_from,multiplier_numerator,multiplier_denominator,checksum,change_reason,created_by) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, create.ZoneID, latest+1, status, create.EffectiveFrom, create.MultiplierNumerator, create.MultiplierDenominator, create.Checksum, create.ChangeReason, create.CreatedBy); err != nil {
		return nil, fmt.Errorf("Mail pricing repo: insert Zone adjustment: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("Mail pricing repo: commit Zone adjustment: %w", err)
	}
	return &entity.MailZoneAdjustmentPublished{ID: id, ZoneID: create.ZoneID, VersionNumber: latest + 1, Status: status, EffectiveFrom: create.EffectiveFrom, MultiplierNumerator: create.MultiplierNumerator, MultiplierDenominator: create.MultiplierDenominator, Checksum: create.Checksum}, nil
}
