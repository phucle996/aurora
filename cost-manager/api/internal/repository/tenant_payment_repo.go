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

type tenantPaymentRepository struct {
	db *pgxpool.Pool
}

func NewTenantPaymentRepository(db *pgxpool.Pool) *tenantPaymentRepository {
	return &tenantPaymentRepository{db: db}
}

func (r *tenantPaymentRepository) GetTenantWalletSummary(
	ctx context.Context,
	tenantID uuid.UUID,
) (*entity.WalletSummary, error) {
	var summary entity.WalletSummary
	err := r.db.QueryRow(ctx, `
		SELECT id, currency, cash_balance, promotional_balance, overdraft_limit,
		       status, version, updated_at
		FROM billing.wallets
		WHERE owner_id=$1 AND owner_type='TENANT'::billing.owner_type AND currency='USD'
	`, tenantID).Scan(
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
		return nil, fmt.Errorf("tenant payment repo: read wallet: %w", err)
	}
	return &summary, nil
}

func (r *tenantPaymentRepository) CreateTenantIntent(
	ctx context.Context,
	command entity.CreateTenantPaymentIntentCommand,
) (*entity.PaymentIntent, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("tenant payment repo: begin intent: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err = tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		command.TenantID.String()+":TENANT:PAYMENT",
	); err != nil {
		return nil, fmt.Errorf("tenant payment repo: lock owner: %w", err)
	}

	var walletID uuid.UUID
	var walletStatus string
	err = tx.QueryRow(ctx, `
		SELECT id, status
		FROM billing.wallets
		WHERE owner_id=$1 AND owner_type='TENANT'::billing.owner_type AND currency=$2
		FOR UPDATE
	`, command.TenantID, command.Currency).Scan(&walletID, &walletStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tenant payment repo: lock wallet: %w", err)
	}
	if walletStatus != entity.WalletStatusPendingActivation &&
		walletStatus != entity.WalletStatusActive {
		return nil, billingTaxonomy.ErrInvalidWallet
	}

	var existing entity.PaymentIntent
	var existingSettledAt *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, wallet_id, actor_user_id, amount_micro_units, currency, provider,
		       COALESCE(provider_payment_id, ''),
		       CASE WHEN status='PENDING' AND expires_at <= NOW() THEN 'EXPIRED' ELSE status END,
		       activates_wallet, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE owner_id=$1 AND owner_type='TENANT'::billing.owner_type
		  AND actor_user_id=$2 AND idempotency_key=$3
		FOR UPDATE
	`, command.TenantID, command.ActorID, command.IdempotencyKey).Scan(
		&existing.ID,
		&existing.WalletID,
		&existing.ActorID,
		&existing.AmountMicroUnits,
		&existing.Currency,
		&existing.Provider,
		&existing.ProviderPaymentID,
		&existing.Status,
		&existing.ActivatesWallet,
		&existing.ExpiresAt,
		&existingSettledAt,
		&existing.CreatedAt,
	)
	if err == nil {
		if existing.AmountMicroUnits != command.Amount ||
			existing.Currency != command.Currency ||
			existing.Provider != command.Provider {
			return nil, billingTaxonomy.ErrIdempotencyConflict
		}
		existing.OwnerID = command.TenantID
		existing.OwnerType = entity.OwnerTypeTenant
		existing.SettledAt = existingSettledAt
		existing.Created = false
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("tenant payment repo: commit intent replay: %w", err)
		}
		return &existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("tenant payment repo: read intent replay: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		UPDATE billing.payment_intents
		SET status='EXPIRED', updated_at=NOW()
		WHERE owner_id=$1 AND owner_type='TENANT'::billing.owner_type
		  AND status='PENDING' AND expires_at <= NOW()
	`, command.TenantID); err != nil {
		return nil, fmt.Errorf("tenant payment repo: expire stale intents: %w", err)
	}

	intent := &entity.PaymentIntent{
		ID:               uuid.New(),
		OwnerID:          command.TenantID,
		OwnerType:        entity.OwnerTypeTenant,
		ActorID:          command.ActorID,
		WalletID:         walletID,
		AmountMicroUnits: command.Amount,
		Currency:         command.Currency,
		Provider:         command.Provider,
		Status:           "PENDING",
		ActivatesWallet:  walletStatus == entity.WalletStatusPendingActivation,
		ExpiresAt:        command.ExpiresAt,
		Created:          true,
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.payment_intents
			(id, wallet_id, owner_id, owner_type, actor_user_id, amount_micro_units, currency,
			 provider, status, activates_wallet, personal_referral_reservation_id,
			 idempotency_key, expires_at)
		VALUES ($1, $2, $3, 'TENANT', $4, $5, $6, $7, 'PENDING', $8, NULL, $9, $10)
		RETURNING created_at
	`, intent.ID, walletID, command.TenantID, command.ActorID, command.Amount,
		command.Currency, command.Provider, intent.ActivatesWallet,
		command.IdempotencyKey, command.ExpiresAt,
	).Scan(&intent.CreatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, billingTaxonomy.ErrIdempotencyConflict
		}
		return nil, fmt.Errorf("tenant payment repo: insert intent: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tenant payment repo: commit intent: %w", err)
	}
	return intent, nil
}

func (r *tenantPaymentRepository) GetTenantIntent(
	ctx context.Context,
	tenantID uuid.UUID,
	intentID uuid.UUID,
) (*entity.PaymentIntent, error) {
	var intent entity.PaymentIntent
	var settledAt *time.Time
	err := r.db.QueryRow(ctx, `
		SELECT id, wallet_id, actor_user_id, amount_micro_units, currency, provider,
		       COALESCE(provider_payment_id, ''),
		       CASE WHEN status='PENDING' AND expires_at <= NOW() THEN 'EXPIRED' ELSE status END,
		       activates_wallet, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE id=$1 AND owner_id=$2 AND owner_type='TENANT'::billing.owner_type
	`, intentID, tenantID).Scan(
		&intent.ID,
		&intent.WalletID,
		&intent.ActorID,
		&intent.AmountMicroUnits,
		&intent.Currency,
		&intent.Provider,
		&intent.ProviderPaymentID,
		&intent.Status,
		&intent.ActivatesWallet,
		&intent.ExpiresAt,
		&settledAt,
		&intent.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPaymentIntentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tenant payment repo: get intent: %w", err)
	}
	intent.OwnerID = tenantID
	intent.OwnerType = entity.OwnerTypeTenant
	intent.SettledAt = settledAt
	return &intent, nil
}

var tenantTopUpLedgerNamespace = uuid.MustParse("c74d3417-514d-5b39-b454-08ad1ea35ee7")

func (r *tenantPaymentRepository) ApplyTenantSettlement(
	ctx context.Context,
	settlement entity.PaymentSettlement,
) (*entity.SettlementResult, error) {
	if settlement.OwnerType != entity.OwnerTypeTenant {
		return nil, billingTaxonomy.ErrSettlementMismatch
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("tenant payment repo: begin settlement: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.payment_webhook_inbox
			(provider, provider_event_id, owner_type, payload_hash, payment_intent_id)
		VALUES ($1, $2, 'TENANT', $3, $4)
		ON CONFLICT (provider, provider_event_id) DO NOTHING
		RETURNING TRUE
	`, settlement.Provider, settlement.ProviderEventID, settlement.PayloadHash,
		settlement.PaymentIntentID).Scan(&inserted)
	replayedEvent := false
	if errors.Is(err, pgx.ErrNoRows) {
		var storedHash, storedOwnerType, status string
		var storedIntentID *uuid.UUID
		if err = tx.QueryRow(ctx, `
			SELECT payload_hash, owner_type::text, status, payment_intent_id
			FROM billing.payment_webhook_inbox
			WHERE provider=$1 AND provider_event_id=$2
			FOR UPDATE
		`, settlement.Provider, settlement.ProviderEventID).Scan(
			&storedHash,
			&storedOwnerType,
			&status,
			&storedIntentID,
		); err != nil {
			return nil, fmt.Errorf("tenant payment repo: read webhook replay: %w", err)
		}
		if storedHash != settlement.PayloadHash ||
			storedOwnerType != string(entity.OwnerTypeTenant) ||
			storedIntentID == nil ||
			*storedIntentID != settlement.PaymentIntentID {
			return nil, billingTaxonomy.ErrWebhookReplayConflict
		}
		if status == "REJECTED" {
			return nil, billingTaxonomy.ErrSettlementMismatch
		}
		replayedEvent = true
	} else if err != nil {
		return nil, fmt.Errorf("tenant payment repo: insert webhook inbox: %w", err)
	}

	var intent entity.PaymentIntent
	err = tx.QueryRow(ctx, `
		SELECT id, wallet_id, owner_id, actor_user_id, amount_micro_units,
		       currency, provider, COALESCE(provider_payment_id, ''), status,
		       activates_wallet, expires_at, settled_at, created_at
		FROM billing.payment_intents
		WHERE id=$1 AND owner_type='TENANT'::billing.owner_type
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
		&intent.ExpiresAt,
		&intent.SettledAt,
		&intent.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='TENANT_INTENT_NOT_FOUND', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("tenant payment repo: reject unknown intent: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("tenant payment repo: commit unknown intent rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrPaymentIntentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tenant payment repo: lock intent: %w", err)
	}
	intent.OwnerType = entity.OwnerTypeTenant
	if intent.Provider != settlement.Provider ||
		intent.AmountMicroUnits != settlement.Amount ||
		intent.Currency != settlement.Currency {
		if _, rejectErr := tx.Exec(ctx, `
			UPDATE billing.payment_webhook_inbox
			SET status='REJECTED', error_code='TENANT_SETTLEMENT_MISMATCH', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("tenant payment repo: reject settlement mismatch: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("tenant payment repo: commit mismatch rejection: %w", commitErr)
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
			return nil, fmt.Errorf("tenant payment repo: reject reused provider payment: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("tenant payment repo: commit provider reuse rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrSettlementMismatch
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("tenant payment repo: check provider payment uniqueness: %w", err)
	}

	var walletStatus string
	var restrictionReason *string
	var cashBalance, promotionalBalance int64
	err = tx.QueryRow(ctx, `
		SELECT status, restriction_reason, cash_balance, promotional_balance
		FROM billing.wallets
		WHERE id=$1 AND owner_id=$2 AND owner_type='TENANT'::billing.owner_type
		FOR UPDATE
	`, intent.WalletID, intent.OwnerID).Scan(
		&walletStatus,
		&restrictionReason,
		&cashBalance,
		&promotionalBalance,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrWalletNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tenant payment repo: lock wallet: %w", err)
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
			return nil, fmt.Errorf("tenant payment repo: mark replay applied: %w", err)
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("tenant payment repo: commit replay: %w", err)
		}
		return &entity.SettlementResult{
			PaymentIntentID:    intent.ID,
			WalletID:           intent.WalletID,
			OwnerID:            intent.OwnerID,
			OwnerType:          entity.OwnerTypeTenant,
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
			SET status='REJECTED', error_code='TENANT_WALLET_NOT_CREDITABLE', processed_at=NOW()
			WHERE provider=$1 AND provider_event_id=$2
		`, settlement.Provider, settlement.ProviderEventID); rejectErr != nil {
			return nil, fmt.Errorf("tenant payment repo: reject invalid wallet: %w", rejectErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("tenant payment repo: commit invalid wallet rejection: %w", commitErr)
		}
		return nil, billingTaxonomy.ErrInvalidWallet
	}

	cashBalance += settlement.Amount
	nextWalletStatus := walletStatus
	walletActivated := walletStatus == entity.WalletStatusSuspended && restrictionReason != nil && *restrictionReason == "CREDIT_EXHAUSTED"
	if walletActivated {
		nextWalletStatus = entity.WalletStatusActive
	}
	// [COMMENT]: A provider callback can credit an administratively suspended
	// wallet, but it must never lift the suspension.
	var walletVersion int64
	if err = tx.QueryRow(ctx, `
		UPDATE billing.wallets
		SET cash_balance=$1, status=$2::billing.wallet_lifecycle_status,
		    restriction_reason=CASE WHEN $2='ACTIVE' THEN NULL ELSE restriction_reason END,
		    status_changed_at=CASE WHEN status::text IS DISTINCT FROM $2 THEN NOW() ELSE status_changed_at END,
		    version=version+1, updated_at=NOW()
		WHERE id=$3
		RETURNING version
	`, cashBalance, nextWalletStatus, intent.WalletID).Scan(&walletVersion); err != nil {
		return nil, fmt.Errorf("tenant payment repo: credit cash: %w", err)
	}
	if walletStatus == entity.WalletStatusPendingActivation {
		if _, err = tx.Exec(ctx, `
			INSERT INTO billing.storage_pending_activation_reconcile
				(wallet_id, owner_id, owner_type, target_wallet_version, status, updated_at)
			VALUES ($1,$2,'TENANT',$3,'PENDING',NOW())
			ON CONFLICT (wallet_id) DO UPDATE
			SET owner_id=EXCLUDED.owner_id, owner_type=EXCLUDED.owner_type,
				target_wallet_version=EXCLUDED.target_wallet_version,
				status='PENDING', last_error=NULL, updated_at=NOW()
		`, intent.WalletID, intent.OwnerID, walletVersion); err != nil {
			return nil, fmt.Errorf("tenant payment repo: queue storage activation reconciliation: %w", err)
		}
	}
	admissionMode := "SUSPEND_BILLABLE"
	var admissionReason any = "ADMINISTRATIVE"
	if nextWalletStatus == entity.WalletStatusActive {
		admissionMode = "ALLOW"
		admissionReason = nil
	} else if walletStatus == entity.WalletStatusPendingActivation {
		admissionReason = "NOT_ACTIVATED"
	} else if restrictionReason != nil && *restrictionReason == "CREDIT_EXHAUSTED" {
		admissionReason = "CREDIT_EXHAUSTED"
	}

	topUpLedgerID := uuid.NewSHA1(tenantTopUpLedgerNamespace, intent.ID[:])
	if _, err = tx.Exec(ctx, `
		INSERT INTO billing.wallet_ledger_entries
			(id, wallet_id, owner_id, owner_type, actor_user_id, amount_micro_units,
			 cash_balance_after, promotional_balance_after, currency,
			 entry_type, reference_id, description, occurred_at)
		VALUES ($1, $2, $3, 'TENANT', $4, $5, $6, $7, $8,
		        'TOP_UP', $9, 'Verified tenant payment settlement', $10)
	`, topUpLedgerID, intent.WalletID, intent.OwnerID, intent.ActorID,
		settlement.Amount, cashBalance, promotionalBalance, settlement.Currency,
		settlement.ProviderPaymentID, settlement.SettledAt); err != nil {
		return nil, fmt.Errorf("tenant payment repo: insert top-up ledger: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO billing.wallet_admission_outbox
			(event_id, wallet_id, owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at)
		VALUES ($1,$2,$3,'TENANT',$4,$5,$6,NOW())
	`, uuid.New(), intent.WalletID, intent.OwnerID, walletVersion, admissionMode, admissionReason); err != nil {
		return nil, fmt.Errorf("tenant payment repo: write wallet admission outbox: %w", err)
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
		return nil, fmt.Errorf("tenant payment repo: mark intent settled: %w", err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE billing.payment_webhook_inbox
		SET status='APPLIED', processed_at=NOW()
		WHERE provider=$1 AND provider_event_id=$2
	`, settlement.Provider, settlement.ProviderEventID); err != nil {
		return nil, fmt.Errorf("tenant payment repo: mark webhook applied: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
			return nil, billingTaxonomy.ErrConflict
		}
		return nil, fmt.Errorf("tenant payment repo: commit settlement: %w", err)
	}
	return &entity.SettlementResult{
		PaymentIntentID:    intent.ID,
		WalletID:           intent.WalletID,
		OwnerID:            intent.OwnerID,
		OwnerType:          entity.OwnerTypeTenant,
		ActorID:            intent.ActorID,
		WalletStatus:       nextWalletStatus,
		CashBalance:        cashBalance,
		PromotionalBalance: promotionalBalance,
		WalletActivated:    walletActivated,
		Replayed:           replayedEvent,
	}, nil
}
