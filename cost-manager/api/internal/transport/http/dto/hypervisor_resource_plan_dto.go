package dto

import "time"

type CreateHypervisorResourcePlanRequest struct {
	Code          string    `json:"code" binding:"required"`
	DisplayName   string    `json:"display_name" binding:"required"`
	Description   string    `json:"description"`
	CPUCores      string    `json:"cpu_cores" binding:"required"`
	MemoryMIB     string    `json:"memory_mib" binding:"required"`
	BootDiskGIB   string    `json:"boot_disk_gib" binding:"required"`
	EffectiveFrom time.Time `json:"effective_from" binding:"required"`
	ChangeReason  string    `json:"change_reason" binding:"required"`
}

type PublishHypervisorResourcePlanRevisionRequest struct {
	ExpectedLatestRevision string    `json:"expected_latest_revision" binding:"required"`
	CPUCores               string    `json:"cpu_cores" binding:"required"`
	MemoryMIB              string    `json:"memory_mib" binding:"required"`
	BootDiskGIB            string    `json:"boot_disk_gib" binding:"required"`
	EffectiveFrom          time.Time `json:"effective_from" binding:"required"`
	ChangeReason           string    `json:"change_reason" binding:"required"`
}
