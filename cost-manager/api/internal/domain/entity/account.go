package entity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: OwnerType là principal chịu trách nhiệm thanh toán, không phải actor thực hiện request.
type OwnerType string

const (
	OwnerTypePersonal OwnerType = "PERSONAL"
	OwnerTypeTenant   OwnerType = "TENANT"
)

// [COMMENT]: FreeTierActivation là command idempotent do trusted edge gắn owner identity.
type FreeTierActivation struct {
	OwnerID        uuid.UUID
	OwnerType      OwnerType
	IdempotencyKey string
}

// [COMMENT]: FreeTierAccount là snapshot trả về sau transaction subscription-wallet-grant.
type FreeTierAccount struct {
	SubscriptionID      uuid.UUID
	WalletID            uuid.UUID
	CreditGrantID       uuid.UUID
	OwnerID             uuid.UUID
	OwnerType           OwnerType
	Currency            string
	PromotionalBalance  int64
	GrantedMicroUnits   int64
	SubscriptionStarted time.Time
	Created             bool
}

// WalletSummary là read model tối thiểu cho UI. Các số tiền giữ nguyên micro-unit
// dạng int64; HTTP adapter sẽ serialize thành string để không mất chính xác qua JSON/JS.
type WalletSummary struct {
	WalletID                     uuid.UUID
	Currency                     string
	CashBalanceMicroUnits        int64
	PromotionalBalanceMicroUnits int64
	OverdraftLimitMicroUnits     int64
	Status                       string
	Version                      int64
	UpdatedAt                    time.Time
}
