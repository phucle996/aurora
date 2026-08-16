package entity

import (
	"time"

	"github.com/google/uuid"
)

type StorageZoneAdjustmentListQuery struct {
	ZoneID uuid.UUID
	Limit  int
}

type StorageZoneAdjustmentListItem struct {
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

type StorageZoneAdjustmentListResult struct {
	ZoneID     uuid.UUID
	Items      []StorageZoneAdjustmentListItem
	HasMore    bool
	ObservedAt time.Time
}
