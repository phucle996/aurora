package entity

import (
	"time"

	"github.com/google/uuid"
)

// PricingModel định nghĩa mô hình tính giá: Lũy tiến theo đơn vị (PROGRESSIVE_UNIT) hoặc Gói cố định (FIXED_BUNDLE).
type PricingModel string

const (
	PricingModelProgressiveUnit PricingModel = "PROGRESSIVE_UNIT"
	PricingModelFixedBundle     PricingModel = "FIXED_BUNDLE"
)

// ChargeKindCode định nghĩa mã phân loại cước phí đo lường tài nguyên.
type ChargeKindCode string

const (
	ChargeKindStorageNetworkIn  ChargeKindCode = "storage.network_in.byte"
	ChargeKindStorageNetworkOut ChargeKindCode = "storage.network_out.byte"
	ChargeKindStorageCapacity   ChargeKindCode = "storage.capacity.gb_hour"
)

// PricingScheduleListItem là flat projection tóm tắt một bảng giá trong danh mục Catalog.
type PricingScheduleListItem struct {
	ID              uuid.UUID
	Code            string
	DisplayName     string
	ChargeKindCode  ChargeKindCode
	PricingModel    PricingModel
	Currency        string
	MetadataVersion int
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// PricingScheduleDetailBracket biểu diễn khoảng phân bậc giá trong chi tiết bảng giá.
type PricingScheduleDetailBracket struct {
	ID                       uuid.UUID
	RangeStartQuantity       int64
	RangeEndQuantity         *int64
	PriceNumeratorMicroUnits int64
	PriceDenominatorQuantity int64
}

// PricingScheduleDetail là flat read projection chứa thông tin chi tiết bảng giá kèm thông tin phiên bản hiệu lực mới nhất.
type PricingScheduleDetail struct {
	ID                        uuid.UUID
	Code                      string
	DisplayName               string
	ChargeKindCode            ChargeKindCode
	PricingModel              PricingModel
	Currency                  string
	MetadataVersion           int
	Status                    string
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	HasLatestVersion          bool
	LatestVersionID           uuid.UUID
	LatestVersionNumber       int
	LatestVersionPricingModel PricingModel
	LatestVersionStatus       string
	LatestEffectiveFrom       time.Time
	LatestEffectiveTo         *time.Time
	LatestChecksum            string
}

// PricingScheduleMetadataCommand là Command cập nhật thông tin hiển thị metadata của bảng giá (OCC qua MetadataVersion).
type PricingScheduleMetadataCommand struct {
	ScheduleCode    string
	MetadataVersion int
	DisplayName     string
}

// PricingScheduleMetadataUpdated là kết quả sau khi cập nhật metadata bảng giá thành công.
type PricingScheduleMetadataUpdated struct {
	ID              uuid.UUID
	Code            string
	DisplayName     string
	MetadataVersion int
	UpdatedAt       time.Time
}

// PricingOutboxRow đại diện cho bản ghi Transactional Outbox phát sinh khi phát hành phiên bản bảng giá mới,
// dùng để đồng bộ bảng giá sang L2 Cache và phát tán sự kiện ra các hệ thống tính cước.
type PricingOutboxRow struct {
	ID                uuid.UUID
	PricingScheduleID uuid.UUID
	VersionID         uuid.UUID
	VersionNumber     int32
	ModuleCode        string
	ChargeKindCode    ChargeKindCode
	EffectiveFrom     time.Time
	Checksum          string
	OccurredAt        time.Time
	ClaimToken        uuid.UUID
	RetryCount        int
}
