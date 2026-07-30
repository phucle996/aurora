package hierarchyEntity

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

type ZoneServiceType string

const (
	ZoneServiceTypeHypervisor     ZoneServiceType = "hypervisor"
	ZoneServiceTypeStorage        ZoneServiceType = "storage"
	ZoneServiceTypeMail           ZoneServiceType = "mail"
	ZoneServiceTypeKubernetes     ZoneServiceType = "kubernetes"
	ZoneServiceTypeAI             ZoneServiceType = "ai"
	ZoneServiceTypeDatabase       ZoneServiceType = "database"
	ZoneServiceTypeManagedService ZoneServiceType = "managed_service"
)

type ListZones struct {
	ID        uuid.UUID
	Code      string
	Name      string
	Location  string
	Status    ZoneStatus
	UpdatedAt time.Time
}

type ListZoneCatalog struct {
	ID     uuid.UUID
	Code   string
	Name   string
	Status ZoneStatus
}

type ResolveZoneByCode struct {
	ID     uuid.UUID
	Code   string
	Name   string
	Status ZoneStatus
	Found  bool
}

type CreateZone struct {
	ID                   uuid.UUID
	Code                 string
	Name                 string
	Location             string
	Description          string
	Status               ZoneStatus
	EnableHypervisor     bool
	EnableStorage        bool
	EnableMail           bool
	EnableKubernetes     bool
	EnableAI             bool
	EnableDatabase       bool
	EnableManagedService bool
	HypervisorServiceID  uuid.UUID
	StorageServiceID     uuid.UUID
	MailServiceID        uuid.UUID
	KubernetesServiceID  uuid.UUID
	AIServiceID          uuid.UUID
	DatabaseServiceID    uuid.UUID
	ManagedServiceID     uuid.UUID
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// GetZoneDetail is one flat database row. Zone fields repeat for each service
// so no transport or aggregate entity is shared across workflows.
type GetZoneDetail struct {
	ZoneID           uuid.UUID
	ZoneCode         string
	ZoneName         string
	ZoneLocation     string
	ZoneDescription  string
	ZoneStatus       ZoneStatus
	ZoneCreatedAt    time.Time
	ZoneUpdatedAt    time.Time
	HasService       bool
	ServiceID        uuid.UUID
	ServiceType      ZoneServiceType
	DesiredState     bool
	ActualState      string
	ServiceCreatedAt time.Time
	ServiceUpdatedAt time.Time
}

type UpdateZoneStatus struct {
	ZoneID       uuid.UUID
	Status       ZoneStatus
	AllowedFrom  []ZoneStatus
	ZoneCode     string
	ZoneName     string
	StateChanged bool
}

type DeleteZone struct {
	ZoneID   uuid.UUID
	ZoneCode string
}

type UpdateZoneService struct {
	ID           uuid.UUID
	ZoneID       uuid.UUID
	ZoneCode     string
	ZoneName     string
	ZoneStatus   ZoneStatus
	ServiceType  ZoneServiceType
	DesiredState bool
	ActualState  string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
