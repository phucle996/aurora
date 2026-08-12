package entity

import (
	"time"

	"github.com/google/uuid"
)

// PricingModel is closed at the catalog boundary. A module must have a
// workflow-local validator before FIXED_BUNDLE can be enabled.
type PricingModel string

const (
	PricingModelProgressiveUnit PricingModel = "PROGRESSIVE_UNIT"
	PricingModelFixedBundle     PricingModel = "FIXED_BUNDLE"
)

type PricingScope string

const (
	PricingScopeGlobal PricingScope = "GLOBAL"
	PricingScopeZone   PricingScope = "ZONE"
)

type ChargeKindCode string

const (
	ChargeKindStorageNetworkIn  ChargeKindCode = "storage.network_in.byte"
	ChargeKindStorageNetworkOut ChargeKindCode = "storage.network_out.byte"
	ChargeKindStorageCapacity   ChargeKindCode = "storage.capacity.gb_hour"
)

// PricingSchedule is the controlled logical identity. Its model and unit are
// derived from the Charge Kind Registry in PostgreSQL, never from a quote or a
// settlement report.
type PricingSchedule struct {
	ID              uuid.UUID
	Code            string
	DisplayName     string
	ChargeKindCode  ChargeKindCode
	PricingModel    PricingModel
	ScopeType       PricingScope
	ZoneID          *uuid.UUID
	Currency        string
	MetadataVersion int
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ScalarBracketInput struct {
	ID                       uuid.UUID
	RangeStartQuantity       int64
	RangeEndQuantity         *int64
	PriceNumeratorMicroUnits int64
	PriceDenominatorQuantity int64
}

type PricingScheduleVersionCreate struct {
	ScheduleCode          string
	ExpectedLatestVersion int
	EffectiveFrom         time.Time
	ChangeReason          string
	CreatedBy             uuid.UUID
	Checksum              string
	Brackets              []ScalarBracketInput
}

type PricingScheduleVersion struct {
	ID                uuid.UUID
	PricingScheduleID uuid.UUID
	VersionNumber     int
	PricingModel      PricingModel
	Status            string
	EffectiveFrom     time.Time
	EffectiveTo       *time.Time
	Checksum          string
	Brackets          []ScalarBracketInput
}

type PricingScheduleDetail struct {
	Schedule      PricingSchedule
	LatestVersion PricingScheduleVersion
}

// PricingSnapshot is immutable read data selected by charge kind, trusted Zone
// and effective time. It is safe to share through the read cache.
type PricingSnapshot struct {
	PricingScheduleID uuid.UUID
	VersionID         uuid.UUID
	Code              string
	ChargeKindCode    ChargeKindCode
	ModuleCode        string
	PricingModel      PricingModel
	ScopeType         PricingScope
	ZoneID            *uuid.UUID
	RawInputUnit      string
	VersionNumber     int
	EffectiveFrom     time.Time
	EffectiveTo       *time.Time
	Checksum          string
	Currency          string
	Brackets          []ScalarBracketInput
}

type PricingScheduleMetadataUpdate struct {
	ScheduleCode    string
	MetadataVersion int
	DisplayName     string
}

// StorageEstimate is a read-only hourly capacity quote. It contains schedule
// lineage so a UI cannot mistake it for a wallet debit or recurring commitment.
type StorageEstimate struct {
	CapacityBytes            int64
	HourlyMicroUnits         int64
	Currency                 string
	PricingScheduleCode      string
	PricingScheduleID        uuid.UUID
	PricingScheduleVersionID uuid.UUID
	PricingVersion           int
	PricingChecksum          string
	PricingEffectiveFrom     time.Time
	EstimatedAt              time.Time
}
