package repository

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/domain/repo"
	"cost-manager/api/pkg/apperr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WalletRepositoryImpl struct {
	pool *pgxpool.Pool
}

func NewWalletRepository(pool *pgxpool.Pool) repo.WalletRepository {
	return &WalletRepositoryImpl{pool: pool}
}

func (r *WalletRepositoryImpl) GetOrCreateWallet(ctx context.Context, ownerID uuid.UUID, ownerType string) (*entity.Wallet, error) {
	query := `
		SELECT id, owner_id, owner_type, balance, currency, overdraft_limit, status, created_at, updated_at
		FROM billing.wallets
		WHERE owner_id = $1 AND owner_type = $2
	`
	var w entity.Wallet
	err := r.pool.QueryRow(ctx, query, ownerID, ownerType).Scan(
		&w.ID, &w.OwnerID, &w.OwnerType, &w.Balance, &w.Currency, &w.OverdraftLimit, &w.Status, &w.CreatedAt, &w.UpdatedAt,
	)
	if err == nil {
		return &w, nil
	}

	if err == pgx.ErrNoRows {
		walletID, err := uuid.NewV7()
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrInternalServer, err, "uuid_generation_failed")
		}

		insertQuery := `
			INSERT INTO billing.wallets (id, owner_id, owner_type, balance, currency, overdraft_limit, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (owner_id, owner_type) DO UPDATE SET updated_at = CURRENT_TIMESTAMP
			RETURNING id, owner_id, owner_type, balance, currency, overdraft_limit, status, created_at, updated_at
		`
		err = r.pool.QueryRow(ctx, insertQuery, walletID, ownerID, ownerType, 0.0, "VND", 0.0, "ACTIVE").Scan(
			&w.ID, &w.OwnerID, &w.OwnerType, &w.Balance, &w.Currency, &w.OverdraftLimit, &w.Status, &w.CreatedAt, &w.UpdatedAt,
		)
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrDatabaseFailed, err, "insert_wallet_failed")
		}
		return &w, nil
	}

	return nil, apperr.Wrap(apperr.ErrDatabaseFailed, err, "query_wallet_failed")
}

func (r *WalletRepositoryImpl) Deposit(ctx context.Context, ownerID uuid.UUID, ownerType string, amount float64, desc string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return apperr.Wrap(apperr.ErrDatabaseFailed, err, "begin_transaction_failed")
	}
	defer tx.Rollback(ctx)

	var walletID uuid.UUID
	var status string
	var currentBalance float64
	selectQuery := `
		SELECT id, balance, status FROM billing.wallets
		WHERE owner_id = $1 AND owner_type = $2
		FOR UPDATE
	`
	err = tx.QueryRow(ctx, selectQuery, ownerID, ownerType).Scan(&walletID, &currentBalance, &status)
	if err != nil {
		return apperr.Wrap(apperr.ErrWalletNotFound, err, "wallet_not_found")
	}

	newBalance := currentBalance + amount
	newStatus := status
	if newBalance > 0 && status == "SUSPENDED" {
		newStatus = "ACTIVE"
	}

	updateQuery := `
		UPDATE billing.wallets
		SET balance = $1, status = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $3
	`
	_, err = tx.Exec(ctx, updateQuery, newBalance, newStatus, walletID)
	if err != nil {
		return apperr.Wrap(apperr.ErrDatabaseFailed, err, "update_wallet_failed")
	}

	txID, err := uuid.NewV7()
	if err != nil {
		return apperr.Wrap(apperr.ErrInternalServer, err, "uuid_generation_failed")
	}
	insertTxQuery := `
		INSERT INTO billing.transactions (id, wallet_id, amount, tx_type, service_type, reference_id, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err = tx.Exec(ctx, insertTxQuery, txID, walletID, amount, "DEPOSIT", "SYSTEM", "deposit-sim", desc)
	if err != nil {
		return apperr.Wrap(apperr.ErrDatabaseFailed, err, "insert_transaction_failed")
	}

	if err := tx.Commit(ctx); err != nil {
		return apperr.Wrap(apperr.ErrDatabaseFailed, err, "commit_transaction_failed")
	}

	return nil
}

func (r *WalletRepositoryImpl) GetTransactions(ctx context.Context, walletID uuid.UUID) ([]entity.Transaction, error) {
	query := `
		SELECT id, wallet_id, amount, tx_type, service_type, reference_id, description, created_at
		FROM billing.transactions
		WHERE wallet_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.pool.Query(ctx, query, walletID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrDatabaseFailed, err, "query_transactions_failed")
	}
	defer rows.Close()

	var list []entity.Transaction
	for rows.Next() {
		var t entity.Transaction
		err := rows.Scan(&t.ID, &t.WalletID, &t.Amount, &t.TxType, &t.ServiceType, &t.ReferenceID, &t.Description, &t.CreatedAt)
		if err != nil {
			return nil, apperr.Wrap(apperr.ErrDatabaseFailed, err, "scan_transaction_failed")
		}
		list = append(list, t)
	}

	return list, nil
}
