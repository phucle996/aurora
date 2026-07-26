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
	"github.com/jackc/pgx/v5/pgconn"
)

func (r *accountRepository) ReserveReferral(
	ctx context.Context,
	command entity.ReserveReferralCommand,
) (*entity.ReferralReservation, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("referral repo: begin reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err = tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		command.OwnerID.String()+":PERSONAL:ONBOARDING",
	); err != nil {
		return nil, fmt.Errorf("referral repo: lock owner: %w", err)
	}

	var walletID uuid.UUID
	var walletStatus string
	err = tx.QueryRow(ctx, `
		SELECT id, status
		FROM billing.wallets
		WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type AND currency='USD'
		FOR UPDATE
	`, command.OwnerID).Scan(&walletID, &walletStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("referral repo: lock wallet: %w", err)
	}
	if walletStatus != entity.WalletStatusPendingActivation {
		return nil, billingTaxonomy.ErrWalletAlreadyActive
	}

	var redeemed bool
	if err = tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM billing.referral_redemptions
			WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type
			  AND redemption_kind='ONBOARDING'
		)
	`, command.OwnerID).Scan(&redeemed); err != nil {
		return nil, fmt.Errorf("referral repo: check prior redemption: %w", err)
	}
	if redeemed {
		return nil, billingTaxonomy.ErrReferralAlreadyRedeemed
	}
	if _, err = tx.Exec(ctx, `
		UPDATE billing.referral_reservations
		SET status='CANCELLED', rejection_reason='RESERVATION_EXPIRED', updated_at=NOW()
		WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type
		  AND redemption_kind='ONBOARDING' AND status='RESERVED' AND expires_at <= NOW()
	`, command.OwnerID); err != nil {
		return nil, fmt.Errorf("referral repo: expire stale reservation: %w", err)
	}

	var existing entity.ReferralReservation
	var existingKey string
	var redeemedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, campaign_id, code_snapshot, status, grant_amount_micro_units,
		       minimum_top_up_micro_units, currency, expires_at, grant_expires_at, redeemed_at,
		       COALESCE(rejection_reason, ''), idempotency_key
		FROM billing.referral_reservations
		WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type
		  AND redemption_kind='ONBOARDING' AND status='RESERVED'
		FOR UPDATE
	`, command.OwnerID).Scan(
		&existing.ID,
		&existing.CampaignID,
		&existing.Code,
		&existing.Status,
		&existing.GrantAmountMicroUnits,
		&existing.MinimumTopUpMicroUnits,
		&existing.Currency,
		&existing.ExpiresAt,
		&existing.GrantExpiresAt,
		&redeemedAt,
		&existing.RejectionReason,
		&existingKey,
	)
	if err == nil {
		existing.RedeemedAt = redeemedAt
		if existingKey == command.IdempotencyKey && existing.Code == command.Code {
			if err = tx.Commit(ctx); err != nil {
				return nil, fmt.Errorf("referral repo: commit reservation replay: %w", err)
			}
			return &existing, nil
		}
		if existing.Status == "REDEEMED" {
			return nil, billingTaxonomy.ErrReferralAlreadyRedeemed
		}
		return nil, billingTaxonomy.ErrReferralAlreadyReserved
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("referral repo: read existing reservation: %w", err)
	}

	var campaign entity.ReferralCampaign
	err = tx.QueryRow(ctx, `
		SELECT id, code, amount_micro_units, minimum_top_up_micro_units,
		       currency, max_redemptions, version, starts_at, ends_at
		FROM billing.promotion_campaigns
		WHERE code=$1
		  AND campaign_type='ONBOARDING_REFERRAL'
		  AND status='ACTIVE'
		  AND starts_at <= NOW()
		  AND (ends_at IS NULL OR NOW() < ends_at)
		FOR UPDATE
	`, command.Code).Scan(
		&campaign.ID,
		&campaign.Code,
		&campaign.AmountMicroUnits,
		&campaign.MinimumTopUpMicroUnits,
		&campaign.Currency,
		&campaign.MaxRedemptions,
		&campaign.Version,
		&campaign.StartsAt,
		&campaign.EndsAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrReferralNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("referral repo: load campaign: %w", err)
	}

	if campaign.MaxRedemptions != nil {
		var occupied int64
		err = tx.QueryRow(ctx, `
			SELECT
			  (SELECT COUNT(*) FROM billing.referral_redemptions WHERE campaign_id=$1)
			  +
			  (SELECT COUNT(*) FROM billing.referral_reservations
			   WHERE campaign_id=$1 AND status='RESERVED' AND expires_at > NOW())
		`, campaign.ID).Scan(&occupied)
		if err != nil {
			return nil, fmt.Errorf("referral repo: count campaign capacity: %w", err)
		}
		if occupied >= *campaign.MaxRedemptions {
			return nil, billingTaxonomy.ErrReferralUnavailable
		}
	}

	expiresAt := command.ExpiresAt
	if campaign.EndsAt != nil && campaign.EndsAt.Before(expiresAt) {
		expiresAt = *campaign.EndsAt
	}
	reservation := &entity.ReferralReservation{
		ID:                     uuid.New(),
		CampaignID:             campaign.ID,
		Code:                   campaign.Code,
		Status:                 "RESERVED",
		GrantAmountMicroUnits:  campaign.AmountMicroUnits,
		MinimumTopUpMicroUnits: campaign.MinimumTopUpMicroUnits,
		Currency:               campaign.Currency,
		ExpiresAt:              expiresAt,
		GrantExpiresAt:         campaign.EndsAt,
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO billing.referral_reservations
			(id, campaign_id, wallet_id, owner_id, owner_type, redemption_kind,
			 status, campaign_version, code_snapshot, grant_amount_micro_units,
			 minimum_top_up_micro_units, currency, grant_expires_at, idempotency_key, expires_at)
		VALUES ($1, $2, $3, $4, 'PERSONAL', 'ONBOARDING', 'RESERVED', $5,
		        $6, $7, $8, $9, $10, $11, $12)
	`, reservation.ID, campaign.ID, walletID, command.OwnerID, campaign.Version,
		campaign.Code, campaign.AmountMicroUnits, campaign.MinimumTopUpMicroUnits,
		campaign.Currency, campaign.EndsAt, command.IdempotencyKey, expiresAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, billingTaxonomy.ErrReferralAlreadyReserved
		}
		return nil, fmt.Errorf("referral repo: insert reservation: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("referral repo: commit reservation: %w", err)
	}
	return reservation, nil
}

func (r *accountRepository) ListReferralCampaigns(
	ctx context.Context,
) ([]entity.ReferralCampaign, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.code, c.name, c.amount_micro_units,
		       c.minimum_top_up_micro_units, c.currency, c.status,
		       c.max_redemptions, c.version, c.starts_at, c.ends_at,
		       c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM billing.referral_redemptions rd WHERE rd.campaign_id=c.id),
		       (SELECT COUNT(*) FROM billing.referral_reservations rr
		        WHERE rr.campaign_id=c.id AND rr.status='RESERVED' AND rr.expires_at > NOW())
		FROM billing.promotion_campaigns c
		WHERE c.campaign_type='ONBOARDING_REFERRAL'
		ORDER BY c.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("referral repo: list campaigns: %w", err)
	}
	defer rows.Close()

	campaigns := make([]entity.ReferralCampaign, 0)
	for rows.Next() {
		var campaign entity.ReferralCampaign
		if err := rows.Scan(
			&campaign.ID,
			&campaign.Code,
			&campaign.Name,
			&campaign.AmountMicroUnits,
			&campaign.MinimumTopUpMicroUnits,
			&campaign.Currency,
			&campaign.Status,
			&campaign.MaxRedemptions,
			&campaign.Version,
			&campaign.StartsAt,
			&campaign.EndsAt,
			&campaign.CreatedAt,
			&campaign.UpdatedAt,
			&campaign.Redemptions,
			&campaign.ActiveReservations,
		); err != nil {
			return nil, fmt.Errorf("referral repo: scan campaign: %w", err)
		}
		campaigns = append(campaigns, campaign)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("referral repo: iterate campaigns: %w", err)
	}
	return campaigns, nil
}

func (r *accountRepository) CreateReferralCampaign(
	ctx context.Context,
	command entity.CreateReferralCampaignCommand,
) (*entity.ReferralCampaign, error) {
	campaign := &entity.ReferralCampaign{
		ID:                     uuid.New(),
		Code:                   command.Code,
		Name:                   command.Name,
		AmountMicroUnits:       command.AmountMicroUnits,
		MinimumTopUpMicroUnits: command.MinimumTopUpMicroUnits,
		Currency:               command.Currency,
		Status:                 "PAUSED",
		MaxRedemptions:         command.MaxRedemptions,
		Version:                1,
		StartsAt:               command.StartsAt,
		EndsAt:                 command.EndsAt,
	}
	err := r.db.QueryRow(ctx, `
		INSERT INTO billing.promotion_campaigns
			(id, code, name, amount_micro_units, currency, starts_at, ends_at,
			 status, campaign_type, minimum_top_up_micro_units, max_redemptions)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'PAUSED',
		        'ONBOARDING_REFERRAL', $8, $9)
		RETURNING created_at, updated_at
	`, campaign.ID, campaign.Code, campaign.Name, campaign.AmountMicroUnits,
		campaign.Currency, campaign.StartsAt, campaign.EndsAt,
		campaign.MinimumTopUpMicroUnits, campaign.MaxRedemptions,
	).Scan(&campaign.CreatedAt, &campaign.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, billingTaxonomy.ErrConflict
		}
		return nil, fmt.Errorf("referral repo: create campaign: %w", err)
	}
	return campaign, nil
}

func (r *accountRepository) UpdateReferralCampaignStatus(
	ctx context.Context,
	command entity.UpdateReferralCampaignStatusCommand,
) (*entity.ReferralCampaign, error) {
	var campaign entity.ReferralCampaign
	err := r.db.QueryRow(ctx, `
		UPDATE billing.promotion_campaigns
		SET status=$1, version=version+1, updated_at=NOW()
		WHERE id=$2 AND campaign_type='ONBOARDING_REFERRAL' AND version=$3
		RETURNING id, code, name, amount_micro_units, minimum_top_up_micro_units,
		          currency, status, max_redemptions, version, starts_at, ends_at,
		          created_at, updated_at
	`, command.Status, command.ID, command.ExpectedVersion).Scan(
		&campaign.ID,
		&campaign.Code,
		&campaign.Name,
		&campaign.AmountMicroUnits,
		&campaign.MinimumTopUpMicroUnits,
		&campaign.Currency,
		&campaign.Status,
		&campaign.MaxRedemptions,
		&campaign.Version,
		&campaign.StartsAt,
		&campaign.EndsAt,
		&campaign.CreatedAt,
		&campaign.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if checkErr := r.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM billing.promotion_campaigns WHERE id=$1 AND campaign_type='ONBOARDING_REFERRAL')`,
			command.ID,
		).Scan(&exists); checkErr != nil {
			return nil, fmt.Errorf("referral repo: check campaign status conflict: %w", checkErr)
		}
		if exists {
			return nil, billingTaxonomy.ErrConflict
		}
		return nil, billingTaxonomy.ErrReferralNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("referral repo: update campaign status: %w", err)
	}
	return &campaign, nil
}
