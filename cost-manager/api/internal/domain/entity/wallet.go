package entity

import (
	"time"

	"github.com/google/uuid"
)

// WalletStatus định nghĩa enum cho trạng thái ví
type WalletStatus string

const (
	WalletActive    WalletStatus = "ACTIVE"
	WalletSuspended WalletStatus = "SUSPENDED"
)

// TxType định nghĩa enum cho loại giao dịch ví
type TxType string

const (
	TxDeposit     TxType = "DEPOSIT"
	TxUsageCharge TxType = "USAGE_CHARGE"
	TxRefund      TxType = "REFUND"
)

// Wallet là domain entity, không chứa JSON tag — serialization thuộc về DTO layer
type Wallet struct {
	ID             uuid.UUID
	OwnerID        uuid.UUID
	OwnerType      OwnerType // 'personal' hoặc 'tenant'
	Balance        float64
	Currency       string
	OverdraftLimit float64
	Status         WalletStatus // 'ACTIVE', 'SUSPENDED'
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Transaction là domain entity thuần — không gắn presentation concern
type Transaction struct {
	ID          uuid.UUID
	WalletID    uuid.UUID
	Amount      float64 // Dương là nạp, Âm là trừ cước
	TxType      TxType  // 'DEPOSIT', 'USAGE_CHARGE', 'REFUND'
	ServiceType ServiceType // 'STORAGE', 'MAIL', 'VM', 'SYSTEM'
	ReferenceID string
	Description string
	CreatedAt   time.Time
}
