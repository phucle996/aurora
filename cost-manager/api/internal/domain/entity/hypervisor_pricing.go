package entity

import (
	"time"

	"github.com/google/uuid"
)

const (
	ChargeKindHypervisorVCPU       ChargeKindCode = "hypervisor.vcpu.allocated_second"
	ChargeKindHypervisorMemoryMIB  ChargeKindCode = "hypervisor.memory_mib.allocated_second"
	ChargeKindHypervisorDiskGIB    ChargeKindCode = "hypervisor.disk_gib.allocated_second"
	ChargeKindHypervisorNetworkIn  ChargeKindCode = "hypervisor.network_in.byte"
	ChargeKindHypervisorNetworkOut ChargeKindCode = "hypervisor.network_out.byte"
)

// HypervisorPricingSnapshot is the immutable Global-base input owned by
// Hypervisor pricing reads, L2 refresh and allocation settlement.
type HypervisorPricingSnapshot struct {
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
	Brackets          []HypervisorPricingSnapshotBracket
}

type HypervisorPricingSnapshotBracket struct {
	ID                       uuid.UUID
	RangeStartQuantity       int64
	RangeEndQuantity         *int64
	PriceNumeratorMicroUnits int64
	PriceDenominatorQuantity int64
}

type HypervisorBasePricePublishCommand struct {
	ScheduleCode          string
	ExpectedLatestVersion int
	EffectiveFrom         time.Time
	ChangeReason          string
	CreatedBy             uuid.UUID
	Checksum              string
}

type HypervisorBasePriceBracketCommand struct {
	RangeStartQuantity       int64
	RangeEndQuantity         *int64
	PriceNumeratorMicroUnits int64
	PriceDenominatorQuantity int64
}

type HypervisorBasePricePublishTarget struct {
	PricingScheduleID uuid.UUID
	ScheduleCode      string
	ChargeKindCode    ChargeKindCode
	PricingModel      PricingModel
	Currency          string
}

type HypervisorBasePricePublished struct {
	ID                uuid.UUID
	PricingScheduleID uuid.UUID
	ChargeKindCode    ChargeKindCode
	VersionNumber     int
	PricingModel      PricingModel
	Status            string
	EffectiveFrom     time.Time
	Checksum          string
}

type HypervisorZoneAdjustmentPublishCommand struct {
	ZoneID                uuid.UUID
	ExpectedLatestVersion int
	EffectiveFrom         time.Time
	ChangeReason          string
	CreatedBy             uuid.UUID
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

type HypervisorZoneAdjustmentPublished struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	Status                string
	EffectiveFrom         time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

type HypervisorZoneAdjustmentSnapshot struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	EffectiveFrom         time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

// HypervisorEstimate is deliberately flat: each workflow output field has one
// owner and no pricing-version entity is nested inside another entity.
type HypervisorEstimate struct {
	CPUCores                  int64
	MemoryMIB                 int64
	DiskGIB                   int64
	VCPUHourlyMicroUnits      int64
	MemoryHourlyMicroUnits    int64
	DiskHourlyMicroUnits      int64
	HourlyMicroUnits          int64
	Monthly730HourMicroUnits  int64
	Currency                  string
	VCPUScheduleCode          string
	VCPUScheduleID            uuid.UUID
	VCPUScheduleVersionID     uuid.UUID
	VCPUVersion               int
	VCPUChecksum              string
	MemoryScheduleCode        string
	MemoryScheduleID          uuid.UUID
	MemoryScheduleVersionID   uuid.UUID
	MemoryVersion             int
	MemoryChecksum            string
	DiskScheduleCode          string
	DiskScheduleID            uuid.UUID
	DiskScheduleVersionID     uuid.UUID
	DiskVersion               int
	DiskChecksum              string
	RateAdjustmentID          *uuid.UUID
	RateAdjustmentVersion     *int
	RateAdjustmentChecksum    *string
	RateAdjustmentNumerator   int64
	RateAdjustmentDenominator int64
	EstimatedAt               time.Time
}
