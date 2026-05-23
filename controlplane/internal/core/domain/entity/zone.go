package coreEntity

import "time"

type ZoneStatus string

const (
	ZoneStatusActive      ZoneStatus = "active"
	ZoneStatusDraining    ZoneStatus = "draining"
	ZoneStatusMaintenance ZoneStatus = "maintenance"
	ZoneStatusDisabled    ZoneStatus = "disabled"
)

type Zone struct {
	ID        string
	Code      string
	Name      string
	Status    ZoneStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ZoneServiceType string

const (
	ZoneServiceTypeMail       ZoneServiceType = "mail"
	ZoneServiceTypeHypervisor ZoneServiceType = "hypervisor"
	ZoneServiceTypeK8s        ZoneServiceType = "k8s"
	ZoneServiceTypeAI         ZoneServiceType = "ai"
)

type ZoneService struct {
	ID          string
	ZoneID      string
	ServiceType ZoneServiceType
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
