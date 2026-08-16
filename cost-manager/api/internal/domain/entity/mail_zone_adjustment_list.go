package entity

import (
	"time"

	"github.com/google/uuid"
)

type MailZoneAdjustmentListQuery struct {
	ZoneID uuid.UUID
	Limit  int
}

type MailZoneAdjustmentListItem struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	Status                string
	EffectiveFrom         time.Time
	EffectiveTo           *time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
	ChangeReason          string
	CreatedBy             uuid.UUID
	CreatedAt             time.Time
	IsLatest              bool
	IsEffective           bool
}

type MailZoneAdjustmentListResult struct {
	ZoneID     uuid.UUID
	Items      []MailZoneAdjustmentListItem
	HasMore    bool
	ObservedAt time.Time
}
