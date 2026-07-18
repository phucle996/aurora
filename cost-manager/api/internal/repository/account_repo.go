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

const freeTierCampaignCode = "FREE_TIER_100_USD"

var promoLedgerNamespace = uuid.MustParse("b944fdea-ce29-5e4c-87cb-5cd8917b18b1")

type accountRepository struct {
	db *pgxpool.Pool
}

// [COMMENT]: NewAccountRepository khởi tạo repository quản lý money mutation.
func NewAccountRepository(db *pgxpool.Pool) *accountRepository {
	return &accountRepository{db: db}
}

type freeTierCatalog struct {
	PackID             uuid.UUID
	CampaignID         uuid.UUID
	AmountMicroUnits   int64
	Currency           string
	CampaignExpiration *time.Time
}

// [COMMENT]: ActivateFreeTier serialize theo owner và commit subscription-wallet-grant-ledger cùng transaction.
func (r *accountRepository) ActivateFreeTier(ctx context.Context, command entity.FreeTierActivation) (*entity.FreeTierAccount, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("account repo: begin activation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Advisory xact lock theo owner chặn hai idempotency key khác nhau cùng thắng trước unique index.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, command.OwnerID.String()+":"+string(command.OwnerType)); err != nil {
		return nil, fmt.Errorf("account repo: lock owner activation: %w", err)
	}

	var existing entity.FreeTierAccount
	var rawOwnerType string
	findErr := tx.QueryRow(ctx, `
		SELECT s.id, w.id, g.id, s.owner_id, s.owner_type::text, w.currency,
		       w.promotional_balance, g.amount_micro_units, s.started_at
		FROM billing.subscriptions s
		JOIN billing.packs p ON p.id = s.pack_id AND p.code = 'FREE_TIER'
		JOIN billing.wallets w ON w.owner_id = s.owner_id AND w.owner_type = s.owner_type
		JOIN billing.credit_grants g ON g.wallet_id = w.id
		JOIN billing.promotion_campaigns c ON c.id = g.campaign_id AND c.code = $1
		WHERE s.idempotency_key = $2 AND s.owner_id = $3 AND s.owner_type = $4::billing.owner_type
	`, freeTierCampaignCode, command.IdempotencyKey, command.OwnerID, string(command.OwnerType)).Scan(
		&existing.SubscriptionID, &existing.WalletID, &existing.CreditGrantID, &existing.OwnerID, &rawOwnerType,
		&existing.Currency, &existing.PromotionalBalance, &existing.GrantedMicroUnits, &existing.SubscriptionStarted,
	)
	existing.OwnerType = entity.OwnerType(rawOwnerType)
	if findErr == nil {
		existing.Created = false
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return nil, fmt.Errorf("account repo: commit idempotent activation: %w", commitErr)
		}
		return &existing, nil
	} else if !errors.Is(findErr, pgx.ErrNoRows) {
		return nil, findErr
	}

	var catalog freeTierCatalog
	err = tx.QueryRow(ctx, `
		SELECT p.id, c.id, c.amount_micro_units, c.currency,
		       CASE WHEN c.ends_at IS NULL THEN NULL ELSE c.ends_at END
		FROM billing.packs p
		JOIN billing.promotion_campaigns c ON c.code = $1
		WHERE p.code = 'FREE_TIER' AND p.status = 'ACTIVE'
		  AND c.status = 'ACTIVE' AND c.starts_at <= NOW()
		  AND (c.ends_at IS NULL OR NOW() < c.ends_at)
		FOR SHARE OF p, c
	`, freeTierCampaignCode).Scan(&catalog.PackID, &catalog.CampaignID, &catalog.AmountMicroUnits, &catalog.Currency, &catalog.CampaignExpiration)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrPackNotActive
	}
	if err != nil {
		return nil, fmt.Errorf("account repo: load free tier catalog: %w", err)
	}

	now := time.Now().UTC()
	subscriptionID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO billing.subscriptions
			(id, owner_id, owner_type, pack_id, status, idempotency_key, started_at)
		VALUES ($1, $2, $3::billing.owner_type, $4, 'ACTIVE', $5, $6)
	`, subscriptionID, command.OwnerID, string(command.OwnerType), catalog.PackID, command.IdempotencyKey, now)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, billingTaxonomy.ErrAlreadySubscribed
		}
		return nil, fmt.Errorf("account repo: insert subscription: %w", err)
	}

	walletID := uuid.New()
	var cashBalance, promotionalBalance int64
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.wallets (id, owner_id, owner_type, currency)
		VALUES ($1, $2, $3::billing.owner_type, $4)
		ON CONFLICT (owner_id, owner_type, currency) DO UPDATE
		SET updated_at = billing.wallets.updated_at
		RETURNING id, cash_balance, promotional_balance
	`, walletID, command.OwnerID, string(command.OwnerType), catalog.Currency).Scan(&walletID, &cashBalance, &promotionalBalance)
	if err != nil {
		return nil, fmt.Errorf("account repo: create wallet: %w", err)
	}

	grantID := uuid.New()
	_, err = tx.Exec(ctx, `
		INSERT INTO billing.credit_grants
			(id, campaign_id, wallet_id, owner_id, owner_type, amount_micro_units, currency, expires_at, idempotency_key)
		VALUES ($1, $2, $3, $4, $5::billing.owner_type, $6, $7, $8, $9)
	`, grantID,
		catalog.CampaignID,
		walletID,
		command.OwnerID,
		string(command.OwnerType),
		catalog.AmountMicroUnits,
		catalog.Currency,
		catalog.CampaignExpiration,
		command.IdempotencyKey,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, billingTaxonomy.ErrAlreadySubscribed
		}
		return nil, fmt.Errorf("account repo: insert promotional grant: %w", err)
	}

	promotionalBalance += catalog.AmountMicroUnits
	var walletVersion int64
	err = tx.QueryRow(ctx, `
		UPDATE billing.wallets
		SET promotional_balance = $1, version = version + 1, updated_at = $2
		WHERE id = $3 AND status = 'ACTIVE'
		RETURNING version
	`, promotionalBalance, now, walletID).Scan(&walletVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrInvalidWallet
	}
	if err != nil {
		return nil, fmt.Errorf("account repo: credit promotional balance: %w", err)
	}
	_ = walletVersion // Version is persisted for OCC/reconciliation even though activation response does not expose it.

	ledgerID := uuid.NewSHA1(promoLedgerNamespace, grantID[:])
	_, err = tx.Exec(ctx, `
		INSERT INTO billing.wallet_ledger_entries
			(id, wallet_id, owner_id, owner_type, amount_micro_units, cash_balance_after,
			 promotional_balance_after, currency, entry_type, reference_id, description, occurred_at)
		VALUES ($1, $2, $3, $4::billing.owner_type, $5, $6, $7, $8,
		        'PROMO_CREDIT', $9, $10, $11)
	`, ledgerID, walletID, command.OwnerID, string(command.OwnerType), catalog.AmountMicroUnits,
		cashBalance, promotionalBalance, catalog.Currency, grantID.String(), "Free Tier promotional credit", now)
	if err != nil {
		return nil, fmt.Errorf("account repo: insert promotional ledger: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
			return nil, billingTaxonomy.ErrConflict
		}
		return nil, fmt.Errorf("account repo: commit activation: %w", err)
	}
	return &entity.FreeTierAccount{
		SubscriptionID:      subscriptionID,
		WalletID:            walletID,
		CreditGrantID:       grantID,
		OwnerID:             command.OwnerID,
		OwnerType:           command.OwnerType,
		Currency:            catalog.Currency,
		PromotionalBalance:  promotionalBalance,
		GrantedMicroUnits:   catalog.AmountMicroUnits,
		SubscriptionStarted: now,
		Created:             true,
	}, nil
}
