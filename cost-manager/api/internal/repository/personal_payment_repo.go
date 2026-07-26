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

type personalPaymentRepository struct {
	db *pgxpool.Pool
}

func NewPersonalPaymentRepository(db *pgxpool.Pool) *personalPaymentRepository {
	return &personalPaymentRepository{db: db}
}

func (r *personalPaymentRepository) GetPersonalWalletSummary(
	ctx context.Context,
	ownerID uuid.UUID,
) (*entity.WalletSummary, error) {
	var summary entity.WalletSummary
	err := r.db.QueryRow(ctx, `
		SELECT id, currency, cash_balance, promotional_balance, overdraft_limit,
		       status, version, updated_at
		FROM billing.wallets
		WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type AND currency='USD'
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
		return nil, fmt.Errorf("personal payment repo: read wallet: %w", err)
	}
	return &summary, nil
}

func (r *personalPaymentRepository) CreatePersonalIntent(
	ctx context.Context,
	command entity.CreatePersonalPaymentIntentCommand,
) (*entity.PaymentIntent, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("personal payment repo: begin intent: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err = tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		command.OwnerID.String()+":PERSONAL:PAYMENT",
	); err != nil {
		return nil, fmt.Errorf("personal payment repo: lock owner: %w", err)
	}

	var walletID uuid.UUID
	var walletStatus string
	err = tx.QueryRow(ctx, `
		SELECT id, status
		FROM billing.wallets
		WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type AND currency=$2
		FOR UPDATE
	`, command.OwnerID, command.Currency).Scan(&walletID, &walletStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal payment repo: lock wallet: %w", err)
	}
	// Suspended/closed wallets cannot create new payment obligations. A provider
	// event for an intent created before suspension is handled separately.
	if walletStatus != entity.WalletStatusPendingActivation &&
		walletStatus != entity.WalletStatusActive {
		return nil, billingTaxonomy.ErrInvalidWallet
	}

	var existing entity.PaymentIntent
	var existingReferralID *uuid.UUID
	var existingSettledAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, wallet_id, actor_user_id, amount_micro_units, currency, provider,
		       COALESCE(provider_payment_id, ''),
		       CASE WHEN status='PENDING' AND expires_at <= NOW() THEN 'EXPIRED' ELSE status END,
		       activates_wallet, referral_reservation_id, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type AND idempotency_key=$2
		FOR UPDATE
	`, command.OwnerID, command.IdempotencyKey).Scan(
		&existing.ID,
		&existing.WalletID,
		&existing.ActorID,
		&existing.AmountMicroUnits,
		&existing.Currency,
		&existing.Provider,
		&existing.ProviderPaymentID,
		&existing.Status,
		&existing.ActivatesWallet,
		&existingReferralID,
		&existing.ExpiresAt,
		&existingSettledAt,
		&existing.CreatedAt,
	)
	if err == nil {
		if existing.ActorID != command.OwnerID ||
			existing.AmountMicroUnits != command.Amount ||
			existing.Currency != command.Currency ||
			existing.Provider != command.Provider {
			return nil, billingTaxonomy.ErrIdempotencyConflict
		}
		existing.OwnerID = command.OwnerID
		existing.OwnerType = entity.OwnerTypePersonal
		existing.ReferralReservationID = existingReferralID
		existing.SettledAt = existingSettledAt
		existing.Created = false
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("personal payment repo: commit intent replay: %w", err)
		}
		return &existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("personal payment repo: read intent replay: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		UPDATE billing.payment_intents
		SET status='EXPIRED', updated_at=NOW()
		WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type
		  AND status='PENDING' AND expires_at <= NOW()
	`, command.OwnerID); err != nil {
		return nil, fmt.Errorf("personal payment repo: expire stale intents: %w", err)
	}

	var referralID *uuid.UUID
	if walletStatus == entity.WalletStatusPendingActivation {
		var reservedID uuid.UUID
		var referralMinimum int64
		var referralCurrency string
		err = tx.QueryRow(ctx, `
			SELECT id, minimum_top_up_micro_units, currency
			FROM billing.referral_reservations
			WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type
			  AND redemption_kind='ONBOARDING' AND status='RESERVED' AND expires_at > NOW()
			FOR UPDATE
		`, command.OwnerID).Scan(&reservedID, &referralMinimum, &referralCurrency)
		if err == nil {
			if command.Amount < referralMinimum || command.Currency != referralCurrency {
				return nil, billingTaxonomy.ErrPreconditionFailed
			}
			referralID = &reservedID
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("personal payment repo: load referral for intent: %w", err)
		}
	}

	intent := &entity.PaymentIntent{
		ID:                    uuid.New(),
		OwnerID:               command.OwnerID,
		OwnerType:             entity.OwnerTypePersonal,
		ActorID:               command.OwnerID,
		WalletID:              walletID,
		AmountMicroUnits:      command.Amount,
		Currency:              command.Currency,
		Provider:              command.Provider,
		Status:                "PENDING",
		ActivatesWallet:       walletStatus == entity.WalletStatusPendingActivation,
		ReferralReservationID: referralID,
		ExpiresAt:             command.ExpiresAt,
		Created:               true,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.payment_intents
			(id, wallet_id, owner_id, owner_type, actor_user_id, amount_micro_units, currency,
			 provider, status, activates_wallet, referral_reservation_id,
			 idempotency_key, expires_at)
		VALUES ($1, $2, $3, 'PERSONAL', $3, $4, $5, $6, 'PENDING', $7, $8, $9, $10)
		RETURNING created_at
	`, intent.ID, walletID, command.OwnerID, command.Amount, command.Currency,
		command.Provider, intent.ActivatesWallet, referralID, command.IdempotencyKey,
		command.ExpiresAt,
	).Scan(&intent.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, billingTaxonomy.ErrIdempotencyConflict
		}
		return nil, fmt.Errorf("personal payment repo: insert intent: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("personal payment repo: commit intent: %w", err)
	}
	return intent, nil
}

func (r *personalPaymentRepository) GetPersonalIntent(
	ctx context.Context,
	ownerID uuid.UUID,
	intentID uuid.UUID,
) (*entity.PaymentIntent, error) {
	var intent entity.PaymentIntent
	var referralID *uuid.UUID
	var settledAt *time.Time
	err := r.db.QueryRow(ctx, `
		SELECT id, wallet_id, actor_user_id, amount_micro_units, currency, provider,
		       COALESCE(provider_payment_id, ''),
		       CASE WHEN status='PENDING' AND expires_at <= NOW() THEN 'EXPIRED' ELSE status END,
		       activates_wallet, referral_reservation_id, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE id=$1 AND owner_id=$2 AND owner_type='PERSONAL'::billing.owner_type
	`, intentID, ownerID).Scan(
		&intent.ID,
		&intent.WalletID,
		&intent.ActorID,
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
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPaymentIntentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal payment repo: get intent: %w", err)
	}
	intent.OwnerID = ownerID
	intent.OwnerType = entity.OwnerTypePersonal
	intent.ReferralReservationID = referralID
	intent.SettledAt = settledAt
	return &intent, nil
}

var (
	personalTopUpLedgerNamespace = uuid.MustParse("c74d3417-514d-5b39-b454-08ad1ea35ee7")
	referralGrantNamespace       = uuid.MustParse("f79c94dd-ff83-59ab-adf7-47fd1d33cbd4")
	referralLedgerNamespace      = uuid.MustParse("80de0063-8de1-58f0-a248-11450647759f")
	referralRedeemNamespace      = uuid.MustParse("03f6540b-4eb5-51f2-8a89-f40a32ab955e")
)

func (r *personalPaymentRepository) ApplyPersonalSettlement(
	ctx context.Context,
	settlement entity.PaymentSettlement,
) (*entity.SettlementResult, error) {
	if settlement.OwnerType != entity.OwnerTypePersonal {
		return nil, billingTaxonomy.ErrSettlementMismatch
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("personal payment repo: begin settlement: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.payment_webhook_inbox
			(provider, provider_event_id, payload_hash, payment_intent_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, provider_event_id) DO NOTHING
		RETURNING TRUE
	`, settlement.Provider, settlement.ProviderEventID, settlement.PayloadHash,
		settlement.PaymentIntentID).Scan(&inserted)
	replayedEvent := false
	if errors.Is(err, pgx.ErrNoRows) {
		var storedHash, status string
		var storedIntentID *uuid.UUID
		if err = tx.QueryRow(ctx, `
			SELECT payload_hash, status, payment_intent_id
			FROM billing.payment_webhook_inbox
			WHERE provider=$1 AND provider_event_id=$2
			FOR UPDATE
		`, settlement.Provider, settlement.ProviderEventID).Scan(
			&storedHash,
			&status,
			&storedIntentID,
		); err != nil {
			return nil, fmt.Errorf("personal payment repo: read webhook replay: %w", err)
		}
		if storedHash != settlement.PayloadHash ||
			storedIntentID == nil ||
			*storedIntentID != settlement.PaymentIntentID {
			return nil, billingTaxonomy.ErrWebhookReplayConflict
		}
		if status == "REJECTED" {
			return nil, billingTaxonomy.ErrSettlementMismatch
		}
		replayedEvent = true
	} else if err != nil {
		return nil, fmt.Errorf("personal payment repo: insert webhook inbox: %w", err)
	}

	var intent entity.PaymentIntent
	var referralID *uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id, wallet_id, owner_id, actor_user_id, amount_micro_units,
		       currency, provider, COALESCE(provider_payment_id, ''), status,
		       activates_wallet, referral_reservation_id, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE id=$1 AND owner_type='PERSONAL'::billing.owner_type
		FOR UPDATE
	`, settlement.PaymentIntentID).Scan(
		&intent.ID,
		&intent.WalletID,
		&intent.OwnerID,
		&intent.ActorID,
		&intent.AmountMicroUnits,
		&intent.Currency,
		&intent.Provider,
		&intent.ProviderPaymentID,
		&intent.Status,
		&intent.ActivatesWallet,
		&referralID,
		&intent.ExpiresAt,
		&intent.SettledAt,
		&intent.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='PERSONAL_INTENT_NOT_FOUND', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("personal payment repo: reject unknown intent: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("personal payment repo: commit unknown intent rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrPaymentIntentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal payment repo: lock intent: %w", err)
	}
	intent.OwnerType = entity.OwnerTypePersonal
	if intent.Provider != settlement.Provider ||
		intent.AmountMicroUnits != settlement.Amount ||
		intent.Currency != settlement.Currency {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='PERSONAL_SETTLEMENT_MISMATCH', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("personal payment repo: reject settlement mismatch: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("personal payment repo: commit mismatch rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrSettlementMismatch
	}

	var conflictingIntent uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id
		FROM billing.payment_intents
		WHERE provider=$1 AND provider_payment_id=$2 AND id<>$3
		LIMIT 1
		FOR UPDATE
	`, settlement.Provider, settlement.ProviderPaymentID, intent.ID).Scan(&conflictingIntent)
	if err == nil {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='PROVIDER_PAYMENT_REUSED', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("personal payment repo: reject reused provider payment: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("personal payment repo: commit provider reuse rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrSettlementMismatch
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("personal payment repo: check provider payment uniqueness: %w", err)
	}

	if intent.ActivatesWallet && referralID != nil {
		// [COMMENT]: The referral owner fence always precedes the wallet row,
		// preventing lock inversion with concurrent reserve and settle requests.
		if _, err = tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			intent.OwnerID.String()+":PERSONAL:ONBOARDING",
		); err != nil {
			return nil, fmt.Errorf("personal payment repo: lock referral owner: %w", err)
		}
	}

	var walletStatus string
	var cashBalance, promotionalBalance int64
	err = tx.QueryRow(ctx, `
		SELECT status, cash_balance, promotional_balance
		FROM billing.wallets
		WHERE id=$1 AND owner_id=$2 AND owner_type='PERSONAL'::billing.owner_type
		FOR UPDATE
	`, intent.WalletID, intent.OwnerID).Scan(
		&walletStatus,
		&cashBalance,
		&promotionalBalance,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("personal payment repo: lock wallet: %w", err)
	}

	if intent.Status == "SETTLED" {
		if intent.ProviderPaymentID != settlement.ProviderPaymentID {
			return nil, billingTaxonomy.ErrSettlementMismatch
		}
		if _, err = tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='APPLIED', processed_at=COALESCE(processed_at, NOW())
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); err != nil {
			return nil, fmt.Errorf("personal payment repo: mark replay applied: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("personal payment repo: commit replay: %w", err)
		}
		return &entity.SettlementResult{
			PaymentIntentID:    intent.ID,
			WalletID:           intent.WalletID,
			OwnerID:            intent.OwnerID,
			OwnerType:          entity.OwnerTypePersonal,
			ActorID:            intent.ActorID,
			WalletStatus:       walletStatus,
			CashBalance:        cashBalance,
			PromotionalBalance: promotionalBalance,
			Replayed:           true,
		}, nil
	}

	const maxInt64Value = int64(^uint64(0) >> 1)
	if walletStatus == entity.WalletStatusClosed ||
		(walletStatus != entity.WalletStatusPendingActivation &&
			walletStatus != entity.WalletStatusActive &&
			walletStatus != entity.WalletStatusSuspended) ||
		(cashBalance > 0 && settlement.Amount > maxInt64Value-cashBalance) {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='PERSONAL_WALLET_NOT_CREDITABLE', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("personal payment repo: reject invalid wallet: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("personal payment repo: commit invalid wallet rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrInvalidWallet
	}

	cashBalance += settlement.Amount
	nextWalletStatus := walletStatus
	walletActivated := walletStatus == entity.WalletStatusPendingActivation
	if walletActivated {
		nextWalletStatus = entity.WalletStatusActive
	}
	// [COMMENT]: Provider settlement can credit suspended funds but must never
	// remove an administrative suspension.
	if _, err = tx.Exec(ctx, `
		UPDATE billing.wallets
		SET cash_balance=$1, status=$2::billing.wallet_lifecycle_status,
		    version=version+1, updated_at=NOW()
		WHERE id=$3
	`, cashBalance, nextWalletStatus, intent.WalletID); err != nil {
		return nil, fmt.Errorf("personal payment repo: credit cash: %w", err)
	}

	topUpLedgerID := uuid.NewSHA1(personalTopUpLedgerNamespace, intent.ID[:])
	if _, err = tx.Exec(ctx, `
		INSERT INTO billing.wallet_ledger_entries
			(id, wallet_id, owner_id, owner_type, actor_user_id, amount_micro_units,
			 cash_balance_after, promotional_balance_after, currency,
			 entry_type, reference_id, description, occurred_at)
		VALUES ($1, $2, $3, 'PERSONAL', $3, $4, $5, $6, $7,
		        'TOP_UP', $8, 'Verified personal payment settlement', $9)
	`, topUpLedgerID, intent.WalletID, intent.OwnerID, settlement.Amount,
		cashBalance, promotionalBalance, settlement.Currency,
		settlement.ProviderPaymentID, settlement.SettledAt); err != nil {
		return nil, fmt.Errorf("personal payment repo: insert top-up ledger: %w", err)
	}

	result := &entity.SettlementResult{
		PaymentIntentID:    intent.ID,
		WalletID:           intent.WalletID,
		OwnerID:            intent.OwnerID,
		OwnerType:          entity.OwnerTypePersonal,
		ActorID:            intent.ActorID,
		WalletStatus:       nextWalletStatus,
		CashBalance:        cashBalance,
		PromotionalBalance: promotionalBalance,
		WalletActivated:    walletActivated,
		Replayed:           replayedEvent,
	}
	if walletActivated && referralID != nil {
		var reservation entity.ReferralReservation
		var reservationOwnerID uuid.UUID
		var reservationOwnerType string
		var alreadyRedeemed bool
		referralErr := tx.QueryRow(ctx, `
			SELECT id, campaign_id, owner_id, owner_type::text, code_snapshot,
			       status, grant_amount_micro_units, minimum_top_up_micro_units,
			       currency, expires_at, grant_expires_at
			FROM billing.referral_reservations
			WHERE id=$1
			FOR UPDATE
		`, *referralID).Scan(
			&reservation.ID,
			&reservation.CampaignID,
			&reservationOwnerID,
			&reservationOwnerType,
			&reservation.Code,
			&reservation.Status,
			&reservation.GrantAmountMicroUnits,
			&reservation.MinimumTopUpMicroUnits,
			&reservation.Currency,
			&reservation.ExpiresAt,
			&reservation.GrantExpiresAt,
		)
		if referralErr != nil && !errors.Is(referralErr, pgx.ErrNoRows) {
			return nil, fmt.Errorf("personal payment repo: lock referral reservation: %w", referralErr)
		}
		if referralErr == nil {
			if redemptionErr := tx.QueryRow(ctx, `
				SELECT EXISTS(
					SELECT 1
					FROM billing.referral_redemptions
					WHERE owner_id=$1 AND owner_type='PERSONAL'::billing.owner_type
					  AND redemption_kind='ONBOARDING'
				)
			`, intent.OwnerID).Scan(&alreadyRedeemed); redemptionErr != nil {
				return nil, fmt.Errorf("personal payment repo: check referral redemption: %w", redemptionErr)
			}
		}

		switch {
		case errors.Is(referralErr, pgx.ErrNoRows):
			result.ReferralRejectReason = "RESERVATION_NOT_FOUND"
		case reservationOwnerID != intent.OwnerID ||
			reservationOwnerType != string(entity.OwnerTypePersonal):
			result.ReferralRejectReason = "OWNER_MISMATCH"
		case reservation.Status != "RESERVED":
			result.ReferralRejectReason = "RESERVATION_NOT_ACTIVE"
		case settlement.SettledAt.After(reservation.ExpiresAt):
			result.ReferralRejectReason = "RESERVATION_EXPIRED"
		case settlement.Amount < reservation.MinimumTopUpMicroUnits ||
			settlement.Currency != reservation.Currency:
			result.ReferralRejectReason = "TOP_UP_REQUIREMENT_NOT_MET"
		case alreadyRedeemed:
			result.ReferralRejectReason = "ONBOARDING_ALREADY_REDEEMED"
		case promotionalBalance > 0 &&
			reservation.GrantAmountMicroUnits > maxInt64Value-promotionalBalance:
			result.ReferralRejectReason = "PROMOTIONAL_BALANCE_OVERFLOW"
		default:
			grantID := uuid.NewSHA1(referralGrantNamespace, reservation.ID[:])
			var grantInserted bool
			grantErr := tx.QueryRow(ctx, `
				INSERT INTO billing.credit_grants
					(id, campaign_id, wallet_id, owner_id, owner_type,
					 amount_micro_units, currency, expires_at, idempotency_key)
				VALUES ($1, $2, $3, $4, 'PERSONAL', $5, $6, $7, $8)
				ON CONFLICT DO NOTHING
				RETURNING TRUE
			`, grantID, reservation.CampaignID, intent.WalletID, intent.OwnerID,
				reservation.GrantAmountMicroUnits, reservation.Currency,
				reservation.GrantExpiresAt, "referral:"+reservation.ID.String()).Scan(&grantInserted)
			if errors.Is(grantErr, pgx.ErrNoRows) {
				result.ReferralRejectReason = "CREDIT_GRANT_CONFLICT"
			} else if grantErr != nil {
				return nil, fmt.Errorf("personal payment repo: insert referral grant: %w", grantErr)
			}

			if grantInserted {
				promotionalBalance += reservation.GrantAmountMicroUnits
				if _, err = tx.Exec(ctx, `
					UPDATE billing.wallets
					SET promotional_balance=$1, version=version+1, updated_at=NOW()
					WHERE id=$2
				`, promotionalBalance, intent.WalletID); err != nil {
					return nil, fmt.Errorf("personal payment repo: credit referral promotion: %w", err)
				}

				referralLedgerID := uuid.NewSHA1(referralLedgerNamespace, reservation.ID[:])
				if _, err = tx.Exec(ctx, `
					INSERT INTO billing.wallet_ledger_entries
						(id, wallet_id, owner_id, owner_type, actor_user_id, amount_micro_units,
						 cash_balance_after, promotional_balance_after, currency,
						 entry_type, reference_id, description, occurred_at)
					VALUES ($1, $2, $3, 'PERSONAL', $3, $4, $5, $6, $7,
					        'PROMO_CREDIT', $8, 'Onboarding referral credit', $9)
				`, referralLedgerID, intent.WalletID, intent.OwnerID,
					reservation.GrantAmountMicroUnits, cashBalance, promotionalBalance,
					reservation.Currency, grantID.String(), settlement.SettledAt); err != nil {
					return nil, fmt.Errorf("personal payment repo: insert referral ledger: %w", err)
				}

				redemptionID := uuid.NewSHA1(referralRedeemNamespace, reservation.ID[:])
				if _, err = tx.Exec(ctx, `
					INSERT INTO billing.referral_redemptions
						(id, reservation_id, campaign_id, wallet_id, owner_id, owner_type,
						 redemption_kind, payment_intent_id, credit_grant_id,
						 amount_micro_units, currency, redeemed_at)
					VALUES ($1, $2, $3, $4, $5, 'PERSONAL', 'ONBOARDING',
					        $6, $7, $8, $9, $10)
				`, redemptionID, reservation.ID, reservation.CampaignID, intent.WalletID,
					intent.OwnerID, intent.ID, grantID, reservation.GrantAmountMicroUnits,
					reservation.Currency, settlement.SettledAt); err != nil {
					var pgErr *pgconn.PgError
					if errors.As(err, &pgErr) && pgErr.Code == "23505" {
						return nil, billingTaxonomy.ErrReferralAlreadyRedeemed
					}
					return nil, fmt.Errorf("personal payment repo: insert referral redemption: %w", err)
				}
				if _, err = tx.Exec(ctx, `
					UPDATE billing.referral_reservations
					SET status='REDEEMED', redeemed_at=$1, updated_at=NOW()
					WHERE id=$2
				`, settlement.SettledAt, reservation.ID); err != nil {
					return nil, fmt.Errorf("personal payment repo: mark referral redeemed: %w", err)
				}
				result.ReferralApplied = true
				result.PromotionalBalance = promotionalBalance
			}
		}

		if result.ReferralRejectReason != "" && reservation.ID != uuid.Nil {
			if _, err = tx.Exec(ctx, `
				UPDATE billing.referral_reservations
				SET status='REJECTED', rejection_reason=$1, updated_at=NOW()
				WHERE id=$2 AND status='RESERVED'
			`, result.ReferralRejectReason, reservation.ID); err != nil {
				return nil, fmt.Errorf("personal payment repo: reject referral reservation: %w", err)
			}
		}
	}

	if _, err = tx.Exec(ctx, `
		UPDATE billing.payment_intents
		SET status='SETTLED', provider_payment_id=$1, settled_at=$2, updated_at=NOW()
		WHERE id=$3
	`, settlement.ProviderPaymentID, settlement.SettledAt, intent.ID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, billingTaxonomy.ErrSettlementMismatch
		}
		return nil, fmt.Errorf("personal payment repo: mark intent settled: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE billing.payment_webhook_inbox
		SET status='APPLIED', processed_at=NOW()
		WHERE provider=$1 AND provider_event_id=$2
	`, settlement.Provider, settlement.ProviderEventID); err != nil {
		return nil, fmt.Errorf("personal payment repo: mark webhook applied: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
			return nil, billingTaxonomy.ErrConflict
		}
		return nil, fmt.Errorf("personal payment repo: commit settlement: %w", err)
	}
	return result, nil
}
