package entity

import (
	"time"

	"github.com/google/uuid"
)

// Wallet là domain entity, không chứa JSON tag — serialization thuộc về DTO layer
type Wallet struct {
	ID             uuid.UUID
	OwnerID        uuid.UUID
	OwnerType      string // 'personal' hoặc 'tenant'
	Balance        float64
	Currency       string
	OverdraftLimit float64
	Status         string // 'ACTIVE', 'SUSPENDED'
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Transaction là domain entity thuần — không gắn presentation concern
type Transaction struct {
	ID          uuid.UUID
	WalletID    uuid.UUID
	Amount      float64 // Dương là nạp, Âm là trừ cước
	TxType      string  // 'DEPOSIT', 'USAGE_CHARGE', 'REFUND'
	ServiceType string  // 'STORAGE', 'MAIL', 'VM', 'SYSTEM'
	ReferenceID string
	Description string
	CreatedAt   time.Time
}
