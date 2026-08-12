package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type tenantAccountRepository struct {
	db *pgxpool.Pool
}

func NewTenantAccountRepository(db *pgxpool.Pool) *tenantAccountRepository {
	return &tenantAccountRepository{db: db}
}

// ApplyTenantWalletProvision commits the owner/actor-specific inbox row and
// zero-balance wallet together. A replay with another actor is a contract
// conflict, even when tenant_id is unchanged.
func (r *tenantAccountRepository) ApplyTenantWalletProvision(
	ctx context.Context,
	eventID uuid.UUID,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	payloadHash string,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tenant account repo: begin wallet provision: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.tenant_wallet_provision_inbox
			(event_id, schema_version, tenant_id, actor_user_id, payload_hash)
		VALUES ($1, 1, $2, $3, $4)
		ON CONFLICT (event_id) DO NOTHING
		RETURNING TRUE
	`, eventID, tenantID, actorID, payloadHash).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		var storedTenantID, storedActorID uuid.UUID
		var storedHash string
		if err := tx.QueryRow(ctx, `
			SELECT tenant_id, actor_user_id, payload_hash
			FROM billing.tenant_wallet_provision_inbox
			WHERE event_id=$1
		`, eventID).Scan(&storedTenantID, &storedActorID, &storedHash); err != nil {
			return fmt.Errorf("tenant account repo: read wallet provision replay: %w", err)
		}
		if storedTenantID != tenantID || storedActorID != actorID || storedHash != payloadHash {
			return fmt.Errorf("tenant account repo: event_id %s reused with different payload", eventID)
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return fmt.Errorf("tenant account repo: insert wallet provision inbox: %w", err)
	}

	// The owner tuple is the second fence when create-tenant delivery is retried
	// with a different event ID after a relay crash.
	var walletID uuid.UUID
	walletCreated := true
	err = tx.QueryRow(ctx, `
		INSERT INTO billing.wallets
			(id, owner_id, owner_type, currency, cash_balance, promotional_balance, status, restriction_reason, status_changed_at)
		VALUES ($1, $2, 'TENANT', 'USD', 0, 0, 'PENDING_ACTIVATION', 'NOT_ACTIVATED', NOW())
		ON CONFLICT (owner_id, owner_type, currency) DO NOTHING
		RETURNING id
	`, uuid.New(), tenantID).Scan(&walletID)
	if errors.Is(err, pgx.ErrNoRows) {
		walletCreated = false
		err = tx.QueryRow(ctx, `SELECT id FROM billing.wallets WHERE owner_id=$1 AND owner_type='TENANT' AND currency='USD' FOR UPDATE`, tenantID).Scan(&walletID)
	}
	if err != nil {
		return fmt.Errorf("tenant account repo: create pending wallet: %w", err)
	}
	if walletCreated {
		if _, err = tx.Exec(ctx, `
		INSERT INTO billing.wallet_admission_outbox
			(event_id, wallet_id, owner_id, owner_type, wallet_version, admission_mode, restriction_reason, effective_at)
		VALUES ($1,$2,$3,'TENANT',1,'SUSPEND_BILLABLE','NOT_ACTIVATED',NOW())
		ON CONFLICT (event_id) DO NOTHING
	`, uuid.New(), walletID, tenantID); err != nil {
			return fmt.Errorf("tenant account repo: write pending wallet admission: %w", err)
		}
	}
	if _, err = tx.Exec(ctx, `
		UPDATE billing.tenant_wallet_provision_inbox
		SET status='APPLIED', processed_at=NOW()
		WHERE event_id=$1
	`, eventID); err != nil {
		return fmt.Errorf("tenant account repo: mark wallet provision applied: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("tenant account repo: commit wallet provision: %w", err)
	}
	return nil
}
