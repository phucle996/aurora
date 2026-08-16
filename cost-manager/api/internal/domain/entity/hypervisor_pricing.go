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
