package entity

import (
	"time"

	"github.com/google/uuid"
)

// WalletAdmissionOutboxRow is the durable, version-fenced projection event.
// It carries no balance or credential data.
type WalletAdmissionOutboxRow struct {
	EventID           uuid.UUID
	OwnerID           uuid.UUID
	OwnerType         OwnerType
	WalletVersion     int64
	AdmissionMode     string
	RestrictionReason *string
	EffectiveAt       time.Time
	ValidUntil        *time.Time
	OccurredAt        time.Time
	ClaimToken        uuid.UUID
}
