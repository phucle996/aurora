package dto

import (
	"time"

	"cost-manager/api/internal/domain/entity"
	"github.com/google/uuid"
)

// WalletResponse là DTO trả về cho client — chứa JSON tags
type WalletResponse struct {
	ID             uuid.UUID `json:"id"`
	OwnerID        uuid.UUID `json:"owner_id"`
	OwnerType      string    `json:"owner_type"`
	Balance        float64   `json:"balance"`
	Currency       string    `json:"currency"`
	OverdraftLimit float64   `json:"overdraft_limit"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// TransactionResponse là DTO trả về cho client
type TransactionResponse struct {
	ID          uuid.UUID `json:"id"`
	WalletID    uuid.UUID `json:"wallet_id"`
	Amount      float64   `json:"amount"`
	TxType      string    `json:"tx_type"`
	ServiceType string    `json:"service_type"`
	ReferenceID string    `json:"reference_id"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// ToWalletResponse map entity → DTO
func ToWalletResponse(w *entity.Wallet) WalletResponse {
	return WalletResponse{
		ID:             w.ID,
		OwnerID:        w.OwnerID,
		OwnerType:      string(w.OwnerType),
		Balance:        w.Balance,
		Currency:       w.Currency,
		OverdraftLimit: w.OverdraftLimit,
		Status:         string(w.Status),
		CreatedAt:      w.CreatedAt,
		UpdatedAt:      w.UpdatedAt,
	}
}

// ToTransactionResponse map entity → DTO
func ToTransactionResponse(t entity.Transaction) TransactionResponse {
	return TransactionResponse{
		ID:          t.ID,
		WalletID:    t.WalletID,
		Amount:      t.Amount,
		TxType:      string(t.TxType),
		ServiceType: string(t.ServiceType),
		ReferenceID: t.ReferenceID,
		Description: t.Description,
		CreatedAt:   t.CreatedAt,
	}
}

// ToTransactionListResponse map slice entity → slice DTO
func ToTransactionListResponse(txs []entity.Transaction) []TransactionResponse {
	out := make([]TransactionResponse, len(txs))
	for i, t := range txs {
		out[i] = ToTransactionResponse(t)
	}
	return out
}
