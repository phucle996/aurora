package entity

import (
	"time"

	"github.com/google/uuid"
)

// PricingScheduleRateStateRow is one flat row from the operator rate-state read. Role
// identifies whether the version is the rate effective at observation time or
// the nearest future scheduled rate; bracket fields belong to that version.
type PricingScheduleRateStateRow struct {
	ScheduleID          uuid.UUID
	Code                string
	DisplayName         string
	ChargeKindCode      ChargeKindCode
	PricingModel        PricingModel
	Currency            string
	MetadataVersion     int
	ObservedAt          time.Time
	LatestVersionNumber *int
	VersionRole         *string
	VersionID           *uuid.UUID
	VersionNumber       *int
	VersionStatus       *string
	EffectiveFrom       *time.Time
	EffectiveTo         *time.Time
	Checksum            *string
	ChangeReason        *string
	BracketID           *uuid.UUID
	RangeStartQuantity  *int64
	RangeEndQuantity    *int64
	PriceNumerator      *int64
	PriceDenominator    *int64
}
