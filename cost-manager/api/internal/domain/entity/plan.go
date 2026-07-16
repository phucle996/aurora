package entity

import (
	"time"

	"github.com/google/uuid"
)

// OwnerType định nghĩa enum cho loại chủ sở hữu ví/sub
type OwnerType string

const (
	OwnerPersonal OwnerType = "personal"
	OwnerTenant   OwnerType = "tenant"
)

// PlanStatus định nghĩa enum cho trạng thái gói cước
type PlanStatus string

const (
	PlanActive     PlanStatus = "ACTIVE"
	PlanDeprecated PlanStatus = "DEPRECATED"
)

// SubStatus định nghĩa enum cho trạng thái thuê bao subscription
type SubStatus string

const (
	SubActive    SubStatus = "ACTIVE"
	SubCancelled SubStatus = "CANCELLED"
	SubExpired   SubStatus = "EXPIRED"
)

// Plan là domain entity đại diện cho một gói cước subscription
type Plan struct {
	ID           uuid.UUID
	Name         string
	Code         string // Unique code, vd: 'STORAGE_BASIC_VN1'
	ServiceType  ServiceType
	ZoneCode     string
	MonthlyPrice float64
	Currency     string
	Status       PlanStatus
	Description  string
	Metrics      []PlanMetric // Quota chi tiết của từng metric trong gói
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// PlanMetric là domain entity đại diện quota của 1 metric trong Plan
type PlanMetric struct {
	ID         uuid.UUID
	PlanID     uuid.UUID
	MetricType MetricType // 'STORAGE_AT_REST' | 'EGRESS_INTERNET' | 'REQUEST_WRITE' | 'REQUEST_READ'
	Quota      float64    // Số lượng được dùng theo đơn vị Unit
	Unit       UnitType   // 'GB' | 'GB_HOUR' | 'PER_1K_OPS'
}

// Subscription là domain entity đại diện đăng ký gói của một owner
type Subscription struct {
	ID          uuid.UUID
	OwnerID     uuid.UUID
	OwnerType   OwnerType
	PlanID      uuid.UUID
	Plan        *Plan // Populated khi cần
	Status      SubStatus
	StartedAt   time.Time
	ExpiresAt   *time.Time
	RenewedAt   *time.Time
	CancelledAt *time.Time
	CreatedAt   time.Time
}
