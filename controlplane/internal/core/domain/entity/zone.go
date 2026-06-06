package coreEntity

import (
	"time"

	"github.com/google/uuid"
)

type ZoneStatus string

const (
	ZoneStatusPlanned     ZoneStatus = "planned"
	ZoneStatusActive      ZoneStatus = "active"
	ZoneStatusDraining    ZoneStatus = "draining"
	ZoneStatusMaintenance ZoneStatus = "maintenance"
	ZoneStatusDisabled    ZoneStatus = "disabled"
)

type Zone struct {
	ID          uuid.UUID
	Code        string
	Name        string
	Location    string
	Description string
	Status      ZoneStatus
	CreatedAt   *time.Time
	UpdatedAt   *time.Time
}

type ZoneDetail struct {
	Zone     Zone
	Services []ZoneService
}

type ZoneCatalog struct {
	ID        string
	Code      string
	Name      string
	UpdatedAt *time.Time
}

type ZoneServiceType string

const (
	ZoneServiceTypeHypervisor ZoneServiceType = "hypervisor"
	ZoneServiceTypeStorage    ZoneServiceType = "storage"
	ZoneServiceTypeMail       ZoneServiceType = "mail"
	ZoneServiceTypeKubernetes ZoneServiceType = "kubernetes"
	ZoneServiceTypeAI         ZoneServiceType = "ai"
	ZoneServiceTypeDatabase   ZoneServiceType = "database"
)

type ZoneService struct {
	ID          uuid.UUID
	ZoneID      uuid.UUID
	ServiceType ZoneServiceType
	Enabled     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CreateZoneInput struct {
	Code             string
	Name             string
	Location         string
	Description      string
	EnableHypervisor bool
	EnableStorage    bool
	EnableMail       bool
	EnableKubernetes bool
	EnableAI         bool
	EnableDatabase   bool
}
