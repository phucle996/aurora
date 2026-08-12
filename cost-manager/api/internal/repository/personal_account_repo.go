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
	"github.com/jackc/pgx/v5/pgxpool"
)

type personalAccountRepository struct {
	db *pgxpool.Pool
}

func NewPersonalAccountRepository(db *pgxpool.Pool) *personalAccountRepository {
	return &personalAccountRepository{db: db}
}

// ApplyPersonalWalletProvision commits the inbox row and zero-balance wallet
// together. The wallet is not spendable until a verified settlement activates it.
func (r *personalAccountRepository) ApplyPersonalWalletProvision(
	ctx context.Context,
	eventID uuid.UUID,
	ownerID uuid.UUID,
	payloadHash string,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("personal account repo: begin wallet provision: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.personal_wallet_provision_inbox
			(event_id, schema_version, user_id, payload_hash)
		VALUES ($1, 1, $2, $3)
		ON CONFLICT (event_id) DO NOTHING
		RETURNING TRUE
	`, eventID, ownerID, payloadHash).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		var storedHash string
		var storedUserID uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT user_id, payload_hash FROM billing.personal_wallet_provision_inbox WHERE event_id=$1`,
			eventID,
		).Scan(&storedUserID, &storedHash); err != nil {
			return fmt.Errorf("personal account repo: read wallet provision replay: %w", err)
		}
		if storedUserID != ownerID || storedHash != payloadHash {
			return fmt.Errorf("personal account repo: event_id %s reused with different payload", eventID)
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return fmt.Errorf("personal account repo: insert wallet provision inbox: %w", err)
	}

	// The owner tuple is a second idempotency boundary when upstream emits two
	// different event IDs for the same verified IAM principal.
	var walletID uuid.UUID
	walletCreated := true
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.wallets
			(id, owner_id, owner_type, currency, cash_balance, promotional_balance, status, restriction_reason, status_changed_at)
		VALUES ($1, $2, 'PERSONAL', 'USD', 0, 0, 'PENDING_ACTIVATION', 'NOT_ACTIVATED', NOW())
		ON CONFLICT (owner_id, owner_type, currency) DO NOTHING
		RETURNING id
	`, uuid.New(), ownerID).Scan(&walletID)
	if errors.Is(err, pgx.ErrNoRows) {
		walletCreated = false
		err = tx.QueryRow(ctx, `SELECT id FROM billing.wallets WHERE owner_id=$1 AND owner_type='PERSONAL' AND currency='USD' FOR UPDATE`, ownerID).Scan(&walletID)
	}
	if err != nil {
		return fmt.Errorf("personal account repo: create pending wallet: %w", err)
	}
	if walletCreated {
		if _, err = tx.Exec(ctx, `
		INSERT INTO billing.wallet_admission_outbox
			(event_id, wallet_id, owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at)
		VALUES ($1,$2,$3,'PERSONAL',1,'SUSPEND_BILLABLE','NOT_ACTIVATED',NOW())
		ON CONFLICT (event_id) DO NOTHING
	`, uuid.New(), walletID, ownerID); err != nil {
			return fmt.Errorf("personal account repo: write pending wallet admission: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, `
		UPDATE billing.personal_wallet_provision_inbox
		SET status='APPLIED', processed_at=NOW()
		WHERE event_id=$1
	`, eventID); err != nil {
		return fmt.Errorf("personal account repo: mark wallet provision applied: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("personal account repo: commit wallet provision: %w", err)
	}
	return nil
}

func (r *personalAccountRepository) getPersonalWalletSummary(
	ctx context.Context,
	ownerID uuid.UUID,
) (*entity.WalletSummary, error) {
	var summary entity.WalletSummary
	err := r.db.QueryRow(ctx, `
		SELECT id, currency, cash_balance, promotional_balance, overdraft_limit,
		       status, version, updated_at
		FROM billing.wallets
		WHERE owner_id = $1
		  AND owner_type = 'PERSONAL'::billing.owner_type
		  AND currency = 'USD'
	`, ownerID).Scan(
		&summary.WalletID,
		&summary.Currency,
		&summary.CashBalanceMicroUnits,
		&summary.PromotionalBalanceMicroUnits,
		&summary.OverdraftLimitMicroUnits,
		&summary.Status,
		&summary.Version,
		&summary.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal account repo: read wallet: %w", err)
	}
	return &summary, nil
}

func (r *personalAccountRepository) GetOnboarding(
	ctx context.Context,
	ownerID uuid.UUID,
	minimumTopUp int64,
) (*entity.OnboardingSnapshot, error) {
	wallet, err := r.getPersonalWalletSummary(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	snapshot := &entity.OnboardingSnapshot{
		Wallet:       *wallet,
		MinimumTopUp: minimumTopUp,
	}

	var reservation entity.ReferralReservation
	var redeemedAt *time.Time
	err = r.db.QueryRow(ctx, `
		SELECT id, campaign_id, code_snapshot,
		       CASE WHEN status='RESERVED' AND expires_at <= NOW() THEN 'CANCELLED' ELSE status END,
		       grant_amount_micro_units,
		       minimum_top_up_micro_units, currency, expires_at, grant_expires_at, redeemed_at,
		       COALESCE(rejection_reason,
		                CASE WHEN status='RESERVED' AND expires_at <= NOW() THEN 'RESERVATION_EXPIRED' END,
		                '')
		FROM billing.personal_referral_reservations
		WHERE user_id=$1
		  AND redemption_kind='ONBOARDING'
		ORDER BY created_at DESC
		LIMIT 1
	`, ownerID).Scan(
		&reservation.ID,
		&reservation.CampaignID,
		&reservation.Code,
		&reservation.Status,
		&reservation.GrantAmountMicroUnits,
		&reservation.MinimumTopUpMicroUnits,
		&reservation.Currency,
		&reservation.ExpiresAt,
		&reservation.GrantExpiresAt,
		&redeemedAt,
		&reservation.RejectionReason,
	)
	if err == nil {
		reservation.RedeemedAt = redeemedAt
		snapshot.Referral = &reservation
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("personal account repo: read referral reservation: %w", err)
	}

	var intent entity.PaymentIntent
	var referralID *uuid.UUID
	var settledAt *time.Time
	err = r.db.QueryRow(ctx, `
		SELECT id, wallet_id, amount_micro_units, currency, provider,
		       COALESCE(provider_payment_id, ''),
		       CASE WHEN status='PENDING' AND expires_at <= NOW() THEN 'EXPIRED' ELSE status END,
		       activates_wallet,
		       personal_referral_reservation_id, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type
		ORDER BY created_at DESC
		LIMIT 1
	`, ownerID).Scan(
		&intent.ID,
		&intent.WalletID,
		&intent.AmountMicroUnits,
		&intent.Currency,
		&intent.Provider,
		&intent.ProviderPaymentID,
		&intent.Status,
		&intent.ActivatesWallet,
		&referralID,
		&intent.ExpiresAt,
		&settledAt,
		&intent.CreatedAt,
	)
	if err == nil {
		intent.OwnerID = ownerID
		intent.ReferralReservationID = referralID
		intent.SettledAt = settledAt
		snapshot.LatestPaymentIntent = &intent
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("personal account repo: read latest payment intent: %w", err)
	}
	return snapshot, nil
}

func (r *personalAccountRepository) ReserveReferral(
	ctx context.Context,
	command entity.ReserveReferralCommand,
) (*entity.ReferralReservation, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("personal account repo: begin referral reservation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err = tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		command.OwnerID.String()+":PERSONAL:ONBOARDING",
	); err != nil {
		return nil, fmt.Errorf("personal account repo: lock referral owner: %w", err)
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
		return nil, fmt.Errorf("personal account repo: lock wallet: %w", err)
	}
	if walletStatus != entity.WalletStatusPendingActivation {
		return nil, billingTaxonomy.ErrWalletAlreadyActive
	}

	var redeemed bool
	if err = tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM billing.personal_referral_redemptions
			WHERE user_id=$1 AND redemption_kind='ONBOARDING'
		)
	`, command.OwnerID).Scan(&redeemed); err != nil {
		return nil, fmt.Errorf("personal account repo: check prior referral redemption: %w", err)
	}
	if redeemed {
		return nil, billingTaxonomy.ErrReferralAlreadyRedeemed
	}
	if _, err = tx.Exec(ctx, `
		UPDATE billing.personal_referral_reservations
		SET status='CANCELLED', rejection_reason='RESERVATION_EXPIRED', updated_at=NOW()
		WHERE user_id=$1 AND redemption_kind='ONBOARDING'
		  AND status='RESERVED' AND expires_at <= NOW()
	`, command.OwnerID); err != nil {
		return nil, fmt.Errorf("personal account repo: expire stale referral reservation: %w", err)
	}

	var existing entity.ReferralReservation
	var existingKey string
	var redeemedAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, campaign_id, code_snapshot, status, grant_amount_micro_units,
		       minimum_top_up_micro_units, currency, expires_at, grant_expires_at, redeemed_at,
		       COALESCE(rejection_reason, ''), idempotency_key
		FROM billing.personal_referral_reservations
		WHERE user_id=$1 AND redemption_kind='ONBOARDING' AND status='RESERVED'
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
				return nil, fmt.Errorf("personal account repo: commit referral reservation replay: %w", err)
			}
			return &existing, nil
		}
		return nil, billingTaxonomy.ErrReferralAlreadyReserved
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("personal account repo: read referral reservation: %w", err)
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
		return nil, fmt.Errorf("personal account repo: load referral campaign: %w", err)
	}

	if campaign.MaxRedemptions != nil {
		var occupied int64
		err = tx.QueryRow(ctx, `
			SELECT
			  (SELECT COUNT(*) FROM billing.personal_referral_redemptions WHERE campaign_id=$1)
			  +
			  (SELECT COUNT(*) FROM billing.personal_referral_reservations
			   WHERE campaign_id=$1 AND status='RESERVED' AND expires_at > NOW())
		`, campaign.ID).Scan(&occupied)
		if err != nil {
			return nil, fmt.Errorf("personal account repo: count referral capacity: %w", err)
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
		INSERT INTO billing.personal_referral_reservations
			(id, campaign_id, wallet_id, user_id, redemption_kind, status,
			 campaign_version, code_snapshot, grant_amount_micro_units,
			 minimum_top_up_micro_units, currency, grant_expires_at,
			 idempotency_key, expires_at)
		VALUES ($1, $2, $3, $4, 'ONBOARDING', 'RESERVED', $5,
		        $6, $7, $8, $9, $10, $11, $12)
	`, reservation.ID, campaign.ID, walletID, command.OwnerID, campaign.Version,
		campaign.Code, campaign.AmountMicroUnits, campaign.MinimumTopUpMicroUnits,
		campaign.Currency, campaign.EndsAt, command.IdempotencyKey, expiresAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, billingTaxonomy.ErrReferralAlreadyReserved
		}
		return nil, fmt.Errorf("personal account repo: insert referral reservation: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("personal account repo: commit referral reservation: %w", err)
	}
	return reservation, nil
}

func (r *personalAccountRepository) ListReferralCampaigns(
	ctx context.Context,
) ([]entity.ReferralCampaign, error) {
	rows, err := r.db.Query(ctx, `
		SELECT c.id, c.code, c.name, c.amount_micro_units,
		       c.minimum_top_up_micro_units, c.currency, c.status,
		       c.max_redemptions, c.version, c.starts_at, c.ends_at,
		       c.created_at, c.updated_at,
		       (SELECT COUNT(*) FROM billing.personal_referral_redemptions rd WHERE rd.campaign_id=c.id),
		       (SELECT COUNT(*) FROM billing.personal_referral_reservations rr
		        WHERE rr.campaign_id=c.id AND rr.status='RESERVED' AND rr.expires_at > NOW())
		FROM billing.promotion_campaigns c
		WHERE c.campaign_type='ONBOARDING_REFERRAL'
		ORDER BY c.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("personal account repo: list referral campaigns: %w", err)
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
			return nil, fmt.Errorf("personal account repo: scan referral campaign: %w", err)
		}
		campaigns = append(campaigns, campaign)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("personal account repo: iterate referral campaigns: %w", err)
	}
	return campaigns, nil
}

func (r *personalAccountRepository) CreateReferralCampaign(
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
		return nil, fmt.Errorf("personal account repo: create referral campaign: %w", err)
	}
	return campaign, nil
}

func (r *personalAccountRepository) UpdateReferralCampaignStatus(
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
			return nil, fmt.Errorf("personal account repo: check referral campaign conflict: %w", checkErr)
		}
		if exists {
			return nil, billingTaxonomy.ErrConflict
		}
		return nil, billingTaxonomy.ErrReferralNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal account repo: update referral campaign: %w", err)
	}
	return &campaign, nil
}
