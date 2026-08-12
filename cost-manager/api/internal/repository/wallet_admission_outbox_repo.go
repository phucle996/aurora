package repository

import (
	"context"
	"fmt"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type walletAdmissionOutboxRepository struct{ db *pgxpool.Pool }

func NewWalletAdmissionOutboxRepository(db *pgxpool.Pool) billingRepoInterface.WalletAdmissionOutboxRepository {
	return &walletAdmissionOutboxRepository{db: db}
}

func (r *walletAdmissionOutboxRepository) ClaimUnpublishedWalletAdmissionBatch(ctx context.Context, limit int, claimToken uuid.UUID) ([]*entity.WalletAdmissionOutboxRow, error) {
	rows, err := r.db.Query(ctx, `
		WITH picked AS (
			SELECT event_id
			FROM billing.wallet_admission_outbox
			WHERE published_at IS NULL
			  AND (claimed_at IS NULL OR claimed_at < NOW() - INTERVAL '1 minute')
			ORDER BY occurred_at, event_id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE billing.wallet_admission_outbox outbox
		SET claim_token = $2, claimed_at = NOW()
		FROM picked
		WHERE outbox.event_id = picked.event_id
		RETURNING outbox.event_id, outbox.wallet_id, outbox.owner_id,
		          outbox.owner_type::text, outbox.wallet_version,
		          outbox.admission_mode, outbox.restriction_reason,
		          outbox.effective_at, outbox.valid_until, outbox.occurred_at,
		          outbox.claim_token`, limit, claimToken)
	if err != nil {
		return nil, fmt.Errorf("wallet admission outbox: claim batch: %w", err)
	}
	defer rows.Close()
	result := make([]*entity.WalletAdmissionOutboxRow, 0)
	for rows.Next() {
		var row entity.WalletAdmissionOutboxRow
		var ownerType string
		if err := rows.Scan(&row.EventID, &row.WalletID, &row.OwnerID, &ownerType, &row.WalletVersion, &row.AdmissionMode, &row.RestrictionReason, &row.EffectiveAt, &row.ValidUntil, &row.OccurredAt, &row.ClaimToken); err != nil {
			return nil, fmt.Errorf("wallet admission outbox: scan: %w", err)
		}
		row.OwnerType = entity.OwnerType(ownerType)
		result = append(result, &row)
	}
	return result, rows.Err()
}

func (r *walletAdmissionOutboxRepository) ListActiveStorageAdmissionTargets(ctx context.Context, ownerID uuid.UUID, ownerType entity.OwnerType) ([]*entity.StorageAdmissionTarget, error) {
	rows, err := r.db.Query(ctx, `
		SELECT resource_id, resource_name, zone_id
		FROM billing.resource_ownership_projection
		WHERE resource_type = 'STORAGE_BUCKET'
		  AND owner_id = $1
		  AND owner_type = $2::billing.owner_type
		  AND effective_to IS NULL
		ORDER BY zone_id, resource_id`, ownerID, ownerType)
	if err != nil {
		return nil, fmt.Errorf("wallet admission outbox: select storage targets: %w", err)
	}
	defer rows.Close()
	targets := make([]*entity.StorageAdmissionTarget, 0)
	for rows.Next() {
		var target entity.StorageAdmissionTarget
		if err := rows.Scan(&target.ResourceID, &target.ResourceName, &target.ZoneID); err != nil {
			return nil, fmt.Errorf("wallet admission outbox: scan storage target: %w", err)
		}
		targets = append(targets, &target)
	}
	return targets, rows.Err()
}

func (r *walletAdmissionOutboxRepository) MarkWalletAdmissionPublished(ctx context.Context, eventID, claimToken uuid.UUID) error {
	if _, err := r.db.Exec(ctx, `UPDATE billing.wallet_admission_outbox SET published_at=NOW(), claim_token=NULL, claimed_at=NULL, last_error=NULL WHERE event_id=$1 AND claim_token=$2`, eventID, claimToken); err != nil {
		return fmt.Errorf("wallet admission outbox: mark published: %w", err)
	}
	return nil
}

func (r *walletAdmissionOutboxRepository) RecordWalletAdmissionError(ctx context.Context, eventID, claimToken uuid.UUID, message string) error {
	if _, err := r.db.Exec(ctx, `UPDATE billing.wallet_admission_outbox SET retry_count=retry_count+1, claim_token=NULL, claimed_at=NULL, last_error=$1 WHERE event_id=$2 AND claim_token=$3`, message, eventID, claimToken); err != nil {
		return fmt.Errorf("wallet admission outbox: record error: %w", err)
	}
	return nil
}
