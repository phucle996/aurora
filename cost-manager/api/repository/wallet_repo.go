package repository

import (
	"context"
	"fmt"

	"cost-manager/api/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WalletRepository interface {
	GetOrCreateWallet(ctx context.Context, ownerID uuid.UUID, ownerType string) (*model.Wallet, error)
	Deposit(ctx context.Context, ownerID uuid.UUID, ownerType string, amount float64, desc string) error
	GetTransactions(ctx context.Context, walletID uuid.UUID) ([]model.Transaction, error)
}

type WalletRepositoryImpl struct {
	pool *pgxpool.Pool
}

func NewWalletRepository(pool *pgxpool.Pool) WalletRepository {
	return &WalletRepositoryImpl{pool: pool}
}

// [COMMENT]: GetOrCreateWallet truy vấn ví của user/workspace. Nếu chưa có, tự động INSERT ví mới
func (r *WalletRepositoryImpl) GetOrCreateWallet(ctx context.Context, ownerID uuid.UUID, ownerType string) (*model.Wallet, error) {
	// [COMMENT]: 1. Thử truy vấn ví hiện tại
	query := `
		SELECT id, owner_id, owner_type, balance, currency, overdraft_limit, status, created_at, updated_at
		FROM billing.wallets
		WHERE owner_id = $1 AND owner_type = $2
	`
	var w model.Wallet
	err := r.pool.QueryRow(ctx, query, ownerID, ownerType).Scan(
		&w.ID, &w.OwnerID, &w.OwnerType, &w.Balance, &w.Currency, &w.OverdraftLimit, &w.Status, &w.CreatedAt, &w.UpdatedAt,
	)
	if err == nil {
		return &w, nil
	}

	// [COMMENT]: 2. Nếu ví chưa tồn tại (pgx.ErrNoRows), tiến hành tạo mới
	if err == pgx.ErrNoRows {
		walletID, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("wallet repo: generate uuid failed: %w", err)
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
			return nil, fmt.Errorf("wallet repo: insert wallet failed: %w", err)
		}
		return &w, nil
	}

	return nil, fmt.Errorf("wallet repo: select wallet failed: %w", err)
}

// [COMMENT]: Deposit thực hiện nạp tiền chạy bên trong transaction nguyên tử
func (r *WalletRepositoryImpl) Deposit(ctx context.Context, ownerID uuid.UUID, ownerType string, amount float64, desc string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("wallet repo: begin tx failed: %w", err)
	}
	defer tx.Rollback(ctx)

	// [COMMENT]: 1. Lấy thông tin ví và LOCK dòng (FOR UPDATE) để tránh race condition nạp đè
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
		return fmt.Errorf("wallet repo: lock wallet for update failed: %w", err)
	}

	// [COMMENT]: 2. Tính toán số dư mới
	newBalance := currentBalance + amount
	newStatus := status
	// [COMMENT]: Nếu ví đang bị SUSPENDED do hết tiền mà nạp dương tiền (> 0), khôi phục trạng thái ACTIVE
	if newBalance > 0 && status == "SUSPENDED" {
		newStatus = "ACTIVE"
	}

	// [COMMENT]: 3. Cập nhật số dư và trạng thái ví
	updateQuery := `
		UPDATE billing.wallets
		SET balance = $1, status = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $3
	`
	_, err = tx.Exec(ctx, updateQuery, newBalance, newStatus, walletID)
	if err != nil {
		return fmt.Errorf("wallet repo: update balance failed: %w", err)
	}

	// [COMMENT]: 4. Ghi nhận lịch sử giao dịch nạp tiền (ledger)
	txID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("wallet repo: generate tx uuid failed: %w", err)
	}
	insertTxQuery := `
		INSERT INTO billing.transactions (id, wallet_id, amount, tx_type, service_type, reference_id, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err = tx.Exec(ctx, insertTxQuery, txID, walletID, amount, "DEPOSIT", "SYSTEM", "deposit-sim", desc)
	if err != nil {
		return fmt.Errorf("wallet repo: insert transaction failed: %w", err)
	}

	// [COMMENT]: Commit transaction
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("wallet repo: commit tx failed: %w", err)
	}

	return nil
}

// [COMMENT]: GetTransactions truy vấn danh sách lịch sử giao dịch của ví
func (r *WalletRepositoryImpl) GetTransactions(ctx context.Context, walletID uuid.UUID) ([]model.Transaction, error) {
	query := `
		SELECT id, wallet_id, amount, tx_type, service_type, reference_id, description, created_at
		FROM billing.transactions
		WHERE wallet_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.pool.Query(ctx, query, walletID)
	if err != nil {
		return nil, fmt.Errorf("wallet repo: select transactions failed: %w", err)
	}
	defer rows.Close()

	var list []model.Transaction
	for rows.Next() {
		var t model.Transaction
		err := rows.Scan(&t.ID, &t.WalletID, &t.Amount, &t.TxType, &t.ServiceType, &t.ReferenceID, &t.Description, &t.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("wallet repo: scan transaction failed: %w", err)
		}
		list = append(list, t)
	}

	return list, nil
}
