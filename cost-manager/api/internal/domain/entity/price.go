package entity

import (
	"time"

	"github.com/google/uuid"
)

// ServiceType định nghĩa enum cho loại dịch vụ
type ServiceType string

const (
	ServiceStorage ServiceType = "STORAGE"
	ServiceVM      ServiceType = "VM"
	ServiceMail    ServiceType = "MAIL"
	ServiceSystem  ServiceType = "SYSTEM"
)

// MetricType định nghĩa enum cho chiều đo lường tài nguyên tiêu thụ
type MetricType string

const (
	MetricStorageAtRest   MetricType = "STORAGE_AT_REST"
	MetricEgressInternet  MetricType = "EGRESS_INTERNET"
	MetricEgressCrossZone MetricType = "EGRESS_CROSS_ZONE"
	MetricRequestWrite    MetricType = "REQUEST_WRITE"
	MetricRequestRead     MetricType = "REQUEST_READ"
	MetricVCPUUsage       MetricType = "VCPU_USAGE"
	MetricRAMUsage        MetricType = "RAM_USAGE"
)

// UnitType định nghĩa enum cho đơn vị đo lường
type UnitType string

const (
	UnitGB        UnitType = "GB"
	UnitGBHour    UnitType = "GB_HOUR"
	UnitPer1kOps  UnitType = "PER_1K_OPS"
	UnitCoreHour  UnitType = "CORE_HOUR"
	UnitRAMGBHour UnitType = "RAM_GB_HOUR"
)

// TierType định nghĩa enum cho tier lưu trữ
type TierType string

const (
	TierStandard TierType = "STANDARD"
	TierCold     TierType = "COLD"
	TierArchive  TierType = "ARCHIVE"
)

// Price là domain entity, không chứa JSON tag
type Price struct {
	ID            uuid.UUID
	ServiceType   ServiceType
	MetricType    MetricType
	ZoneCode      string
	Unit          UnitType
	UnitPrice     float64
	Currency      string
	Tier          TierType
	FreeQuota     float64 // Quota miễn phí (0 = không có free tier)
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	CreatedAt     time.Time
}

// Zone là domain entity thuần
type Zone struct {
	ID     string
	Code   string
	Name   string
	Status string
}
