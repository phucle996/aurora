package entity

import (
	"time"

	"github.com/google/uuid"
)

type PricingModel string

const (
	PricingModelProgressiveUnit PricingModel = "PROGRESSIVE_UNIT"
	PricingModelFixedBundle     PricingModel = "FIXED_BUNDLE"
)

type ChargeKindCode string

const (
	ChargeKindStorageNetworkIn  ChargeKindCode = "storage.network_in.byte"
	ChargeKindStorageNetworkOut ChargeKindCode = "storage.network_out.byte"
	ChargeKindStorageCapacity   ChargeKindCode = "storage.capacity.gb_hour"
)

// Every type below belongs to one workflow. No API workflow consumes the
// result entity of another workflow.

type PricingScheduleListItem struct {
	ID              uuid.UUID
	Code            string
	DisplayName     string
	ChargeKindCode  ChargeKindCode
	PricingModel    PricingModel
	Currency        string
	MetadataVersion int
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type PricingScheduleDetailBracket struct {
	ID                       uuid.UUID
	RangeStartQuantity       int64
	RangeEndQuantity         *int64
	PriceNumeratorMicroUnits int64
	PriceDenominatorQuantity int64
}

// PricingScheduleDetail is one flat read projection. Latest-version fields are
// columns of this workflow result, not a nested version entity.
type PricingScheduleDetail struct {
	ID                        uuid.UUID
	Code                      string
	DisplayName               string
	ChargeKindCode            ChargeKindCode
	PricingModel              PricingModel
	Currency                  string
	MetadataVersion           int
	Status                    string
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	HasLatestVersion          bool
	LatestVersionID           uuid.UUID
	LatestVersionNumber       int
	LatestVersionPricingModel PricingModel
	LatestVersionStatus       string
	LatestEffectiveFrom       time.Time
	LatestEffectiveTo         *time.Time
	LatestChecksum            string
}

type PricingScheduleVersionPublishBracket struct {
	ID                       uuid.UUID
	RangeStartQuantity       int64
	RangeEndQuantity         *int64
	PriceNumeratorMicroUnits int64
	PriceDenominatorQuantity int64
}

type PricingScheduleVersionPublishCommand struct {
	ScheduleCode          string
	ExpectedLatestVersion int
	EffectiveFrom         time.Time
	ChangeReason          string
	CreatedBy             uuid.UUID
	Checksum              string
}

// PublishTarget is workflow-local authority data, not the detail workflow's
// response projection.
type PricingScheduleVersionPublishTarget struct {
	PricingScheduleID uuid.UUID
	ScheduleCode      string
	ChargeKindCode    ChargeKindCode
	PricingModel      PricingModel
	Currency          string
}

type PricingScheduleVersionPublished struct {
	ID                uuid.UUID
	PricingScheduleID uuid.UUID
	VersionNumber     int
	PricingModel      PricingModel
	Status            string
	EffectiveFrom     time.Time
	EffectiveTo       *time.Time
	Checksum          string
}

type PricingSnapshotBracket struct {
	ID                       uuid.UUID
	RangeStartQuantity       int64
	RangeEndQuantity         *int64
	PriceNumeratorMicroUnits int64
	PriceDenominatorQuantity int64
}

// PricingSnapshot is the only Go-side kernel projection. Its bracket rows are
// kernel data and may be composed into this immutable snapshot.
type PricingSnapshot struct {
	PricingScheduleID uuid.UUID
	VersionID         uuid.UUID
	Code              string
	ChargeKindCode    ChargeKindCode
	ModuleCode        string
	PricingModel      PricingModel
	RawInputUnit      string
	VersionNumber     int
	EffectiveFrom     time.Time
	EffectiveTo       *time.Time
	Checksum          string
	Currency          string
	Brackets          []PricingSnapshotBracket
}

type PricingScheduleMetadataCommand struct {
	ScheduleCode    string
	MetadataVersion int
	DisplayName     string
}

type PricingScheduleMetadataUpdated struct {
	ID              uuid.UUID
	Code            string
	DisplayName     string
	MetadataVersion int
	UpdatedAt       time.Time
}

type StorageZoneAdjustmentPublishCommand struct {
	ZoneID                uuid.UUID
	ExpectedLatestVersion int
	EffectiveFrom         time.Time
	ChangeReason          string
	CreatedBy             uuid.UUID
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

type StorageZoneAdjustmentPublished struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	Status                string
	EffectiveFrom         time.Time
	EffectiveTo           *time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

type StorageZoneAdjustmentSnapshot struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	EffectiveFrom         time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

type StorageEstimate struct {
	CapacityBytes             int64
	HourlyMicroUnits          int64
	Currency                  string
	PricingScheduleCode       string
	PricingScheduleID         uuid.UUID
	PricingScheduleVersionID  uuid.UUID
	PricingVersion            int
	PricingChecksum           string
	PricingEffectiveFrom      time.Time
	RateAdjustmentID          *uuid.UUID
	RateAdjustmentVersion     *int
	RateAdjustmentChecksum    *string
	RateAdjustmentNumerator   int64
	RateAdjustmentDenominator int64
	EstimatedAt               time.Time
}
