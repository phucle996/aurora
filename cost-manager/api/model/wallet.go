package model

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: Wallet biểu diễn thực thể Ví tiền trong database billing
type Wallet struct {
	ID             uuid.UUID `json:"id"`
	OwnerID        uuid.UUID `json:"owner_id"`
	OwnerType      string    `json:"owner_type"` // 'personal' hoặc 'tenant'
	Balance        float64   `json:"balance"`
	Currency       string    `json:"currency"`
	OverdraftLimit float64   `json:"overdraft_limit"`
	Status         string    `json:"status"` // 'ACTIVE', 'SUSPENDED'
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// [COMMENT]: Transaction biểu diễn nhật ký giao dịch tài chính của Ví
type Transaction struct {
	ID          uuid.UUID `json:"id"`
	WalletID    uuid.UUID `json:"wallet_id"`
	Amount      float64   `json:"amount"` // Dương là nạp, Âm là trừ cước
	TxType      string    `json:"tx_type"` // 'DEPOSIT', 'USAGE_CHARGE', 'REFUND'
	ServiceType string    `json:"service_type"` // 'STORAGE', 'MAIL', 'VM', 'SYSTEM'
	ReferenceID string    `json:"reference_id"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

// [COMMENT]: Price cấu hình giá dịch vụ
type Price struct {
	ID            uuid.UUID  `json:"id"`
	ServiceType   string     `json:"service_type"`
	UnitPrice     float64    `json:"unit_price"`
	Currency      string     `json:"currency"`
	Tier          string     `json:"tier"`
	EffectiveFrom time.Time  `json:"effective_from"`
	EffectiveTo   *time.Time `json:"effective_to,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}
