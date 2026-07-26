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

type accountRepository struct {
	db *pgxpool.Pool
}

func NewAccountRepository(db *pgxpool.Pool) *accountRepository {
	return &accountRepository{db: db}
}

// ApplyPersonalWalletProvision commits the inbox row and zero-balance wallet
// together. The wallet is not spendable until a verified settlement activates it.
func (r *accountRepository) ApplyPersonalWalletProvision(
	ctx context.Context,
	eventID uuid.UUID,
	ownerID uuid.UUID,
	payloadHash string,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("account repo: begin wallet provision: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.wallet_provision_inbox
			(event_id, schema_version, owner_id, owner_type, payload_hash)
		VALUES ($1, 1, $2, 'PERSONAL', $3)
		ON CONFLICT (event_id) DO NOTHING
		RETURNING TRUE
	`, eventID, ownerID, payloadHash).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		var storedHash, storedOwnerType string
		var storedOwnerID uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT owner_id, owner_type::text, payload_hash FROM billing.wallet_provision_inbox WHERE event_id=$1`,
			eventID,
		).Scan(&storedOwnerID, &storedOwnerType, &storedHash); err != nil {
			return fmt.Errorf("account repo: read wallet provision replay: %w", err)
		}
		if storedOwnerID != ownerID || storedOwnerType != string(entity.OwnerTypePersonal) || storedHash != payloadHash {
			return fmt.Errorf("account repo: event_id %s reused with different payload", eventID)
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return fmt.Errorf("account repo: insert wallet provision inbox: %w", err)
	}

	// The owner tuple is a second idempotency boundary when upstream emits two
	// different event IDs for the same verified IAM principal.
	_, err = tx.Exec(ctx, `
		INSERT INTO billing.wallets
			(id, owner_id, owner_type, currency, cash_balance, promotional_balance, status)
		VALUES ($1, $2, 'PERSONAL', 'USD', 0, 0, 'PENDING_ACTIVATION')
		ON CONFLICT (owner_id, owner_type, currency) DO NOTHING
	`, uuid.New(), ownerID)
	if err != nil {
		return fmt.Errorf("account repo: create pending personal wallet: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE billing.wallet_provision_inbox
		SET status='APPLIED', processed_at=NOW()
		WHERE event_id=$1
	`, eventID); err != nil {
		return fmt.Errorf("account repo: mark wallet provision applied: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("account repo: commit wallet provision: %w", err)
	}
	return nil
}

// ApplyTenantWalletProvision is intentionally independent from personal
// onboarding: tenant creation never reserves or grants promotional balance.
func (r *accountRepository) ApplyTenantWalletProvision(
	ctx context.Context,
	eventID uuid.UUID,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	payloadHash string,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("account repo: begin tenant wallet provision: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.wallet_provision_inbox
			(event_id, schema_version, owner_id, owner_type, actor_user_id, payload_hash)
		VALUES ($1, 1, $2, 'TENANT', $3, $4)
		ON CONFLICT (event_id) DO NOTHING
		RETURNING TRUE
	`, eventID, tenantID, actorID, payloadHash).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		var storedOwnerID, storedActorID uuid.UUID
		var storedOwnerType, storedHash string
		if err := tx.QueryRow(ctx, `
			SELECT owner_id, owner_type::text, actor_user_id, payload_hash
			FROM billing.wallet_provision_inbox
			WHERE event_id=$1
		`, eventID).Scan(&storedOwnerID, &storedOwnerType, &storedActorID, &storedHash); err != nil {
			return fmt.Errorf("account repo: read tenant wallet provision replay: %w", err)
		}
		if storedOwnerID != tenantID || storedOwnerType != string(entity.OwnerTypeTenant) ||
			storedActorID != actorID || storedHash != payloadHash {
			return fmt.Errorf("account repo: tenant event_id %s reused with different payload", eventID)
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return fmt.Errorf("account repo: insert tenant wallet provision inbox: %w", err)
	}

	// The owner tuple is the second fence when create-tenant delivery is retried
	// with a different event ID after a relay crash.
	if _, err = tx.Exec(ctx, `
		INSERT INTO billing.wallets
			(id, owner_id, owner_type, currency, cash_balance, promotional_balance, status)
		VALUES ($1, $2, 'TENANT', 'USD', 0, 0, 'PENDING_ACTIVATION')
		ON CONFLICT (owner_id, owner_type, currency) DO NOTHING
	`, uuid.New(), tenantID); err != nil {
		return fmt.Errorf("account repo: create pending tenant wallet: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE billing.wallet_provision_inbox
		SET status='APPLIED', processed_at=NOW()
		WHERE event_id=$1
	`, eventID); err != nil {
		return fmt.Errorf("account repo: mark tenant wallet provision applied: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("account repo: commit tenant wallet provision: %w", err)
	}
	return nil
}

func (r *accountRepository) GetPersonalWalletSummary(
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
		return nil, fmt.Errorf("account repo: read personal wallet: %w", err)
	}
	return &summary, nil
}

func (r *accountRepository) GetOnboarding(
	ctx context.Context,
	ownerID uuid.UUID,
	minimumTopUp int64,
) (*entity.OnboardingSnapshot, error) {
	wallet, err := r.GetPersonalWalletSummary(ctx, ownerID)
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
		FROM billing.referral_reservations
		WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type
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
		return nil, fmt.Errorf("account repo: read referral reservation: %w", err)
	}

	var intent entity.PaymentIntent
	var referralID *uuid.UUID
	var settledAt *time.Time
	err = r.db.QueryRow(ctx, `
		SELECT id, wallet_id, amount_micro_units, currency, provider,
		       COALESCE(provider_payment_id, ''),
		       CASE WHEN status='PENDING' AND expires_at <= NOW() THEN 'EXPIRED' ELSE status END,
		       activates_wallet,
		       referral_reservation_id, expires_at, settled_at, created_at
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
		return nil, fmt.Errorf("account repo: read latest payment intent: %w", err)
	}
	return snapshot, nil
}
