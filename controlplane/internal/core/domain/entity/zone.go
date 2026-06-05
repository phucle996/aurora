package coreEntity

import "time"

type ZoneStatus string

const (
	ZoneStatusPlanned     ZoneStatus = "planned"
	ZoneStatusActive      ZoneStatus = "active"
	ZoneStatusDraining    ZoneStatus = "draining"
	ZoneStatusMaintenance ZoneStatus = "maintenance"
	ZoneStatusDisabled    ZoneStatus = "disabled"
)

type Zone struct {
	ID        string
	Code      string
	Name      string
	Location  string
	Status    ZoneStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

type ZoneCatalog struct {
	ID   string
	Code string
	Name string
}

type ZoneServiceType string

const (
	ZoneServiceTypeHypervisor ZoneServiceType = "hypervisor"
	ZoneServiceTypeStorage    ZoneServiceType = "storage"
	ZoneServiceTypeMail       ZoneServiceType = "mail"
	ZoneServiceTypeK8s        ZoneServiceType = "k8s"
	ZoneServiceTypeAI         ZoneServiceType = "ai"
	ZoneServiceTypeDatabase   ZoneServiceType = "database"
)

type ZoneService struct {
	ID          string
	ZoneID      string
	ServiceType ZoneServiceType
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CreateZoneInput struct {
	Code             string
	Name             string
	Location         string
	EnableHypervisor bool
	EnableStorage    bool
	EnableMail       bool
	EnableK8s        bool
	EnableAI         bool
}
