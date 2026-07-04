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

// RPCZone đại diện cho dữ liệu tối giản của một Zone phục vụ đồng bộ qua RPC biên (ACR).
// Thiết kế này giúp giảm thiểu tải lượng truyền tải mạng và tối ưu bộ nhớ.
type RPCZone struct {
	ID     uuid.UUID  // ID định danh duy nhất của zone
	Code   string     // Mã code viết tắt của zone (e.g. vn-hn-1)
	Name   string     // Tên hiển thị đầy đủ của zone
	Status ZoneStatus // Trạng thái vận hành hiện tại của zone
}

type ZoneDetail struct {
	Zone     Zone
	Services []ZoneService
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

// ZoneService defines the configuration and operational health status of a service inside a specific zone.
type ZoneService struct {
	ID           uuid.UUID
	ZoneID       uuid.UUID
	ServiceType  ZoneServiceType
	DesiredState bool   // [COMMENT]: Trạng thái cấu hình mong muốn (true: enable, false: disable)
	ActualState  string // [COMMENT]: Trạng thái vận hành thực tế nhận từ Dataplane agent (healthy, degraded, down, unknown)
	CreatedAt    time.Time
	UpdatedAt    time.Time
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
