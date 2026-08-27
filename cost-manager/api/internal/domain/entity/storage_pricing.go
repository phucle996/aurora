package entity

import (
	"time"

	"github.com/google/uuid"
)

// StorageBasePricePublishBracket biểu diễn khoảng phân bậc giá cơ sở (Bracket Tier) của dịch vụ lưu trữ:
// - Khoảng dung lượng: `RangeStartQuantity` -> `RangeEndQuantity` (nil = không giới hạn).
// - Đơn giá: `PriceNumeratorMicroUnits / PriceDenominatorQuantity` (micro-units USD / Byte).
type StorageBasePricePublishBracket struct {
	ID                       uuid.UUID
	RangeStartQuantity       int64
	RangeEndQuantity         *int64
	PriceNumeratorMicroUnits int64
	PriceDenominatorQuantity int64
}

// StorageBasePricePublishCommand là Command phát hành phiên bản giá cơ sở mới cho Storage:
// - `ExpectedLatestVersion`: Kiểm soát Concurrency OCC (Optimistic Concurrency Control).
// - `Checksum`: Mã băm SHA-256 xác thực tính toàn vẹn của bảng giá và các brackets.
type StorageBasePricePublishCommand struct {
	ScheduleCode          string
	ExpectedLatestVersion int
	EffectiveFrom         time.Time
	ChangeReason          string
	CreatedBy             uuid.UUID
	Checksum              string
}

// StorageBasePricePublishTarget là flat authority projection khi publish giá cơ sở.
type StorageBasePricePublishTarget struct {
	PricingScheduleID uuid.UUID
	ScheduleCode      string
	ChargeKindCode    ChargeKindCode
	PricingModel      PricingModel
	Currency          string
}

// StorageBasePricePublished là kết quả sau khi phát hành phiên bản giá cơ sở thành công.
type StorageBasePricePublished struct {
	ID                uuid.UUID
	PricingScheduleID uuid.UUID
	ChargeKindCode    ChargeKindCode
	VersionNumber     int
	PricingModel      PricingModel
	Status            string
	EffectiveFrom     time.Time
	EffectiveTo       *time.Time
	Checksum          string
}

// StoragePricingSnapshot là snapshot bất biến của bảng giá Storage cơ sở, dùng cho đọc giá, nạp Cache L2 và quyết toán chi phí.
type StoragePricingSnapshot struct {
	PricingScheduleID uuid.UUID
	VersionID         uuid.UUID
	Code              string
	ChargeKindCode    ChargeKindCode
	ModuleCode        string
	PricingModel      PricingModel
	RawInputUnit      string
	VersionNumber     int
	EffectiveFrom     time.Time
	EffectiveTo       *time.Time
	Checksum          string
	Currency          string
	Brackets          []StoragePricingSnapshotBracket
}

// StoragePricingSnapshotBracket là bracket phân bậc nằm trong Snapshot giá Storage.
type StoragePricingSnapshotBracket struct {
	ID                       uuid.UUID
	RangeStartQuantity       int64
	RangeEndQuantity         *int64
	PriceNumeratorMicroUnits int64
	PriceDenominatorQuantity int64
}

// StorageZoneAdjustmentPublishCommand là Command phát hành hệ số điều chỉnh giá theo Zone (Zone Rate Adjustment) cho Storage.
type StorageZoneAdjustmentPublishCommand struct {
	ZoneID                uuid.UUID
	ExpectedLatestVersion int
	EffectiveFrom         time.Time
	ChangeReason          string
	CreatedBy             uuid.UUID
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

// StorageZoneAdjustmentPublished là kết quả sau khi phát hành hệ số điều chỉnh Zone cho Storage.
type StorageZoneAdjustmentPublished struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	Status                string
	EffectiveFrom         time.Time
	EffectiveTo           *time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

// StorageZoneAdjustmentSnapshot là snapshot bất biến của hệ số điều chỉnh giá theo Zone đang có hiệu lực.
type StorageZoneAdjustmentSnapshot struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	EffectiveFrom         time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

// StorageZoneAdjustmentListQuery là tham số truy vấn lịch sử các phiên bản hệ số điều chỉnh giá Zone của Storage.
type StorageZoneAdjustmentListQuery struct {
	ZoneID uuid.UUID
	Limit  int
}

// StorageZoneAdjustmentListItem là thông tin tóm tắt của 1 phiên bản hệ số điều chỉnh Zone trong danh sách lịch sử.
type StorageZoneAdjustmentListItem struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	Status                string
	EffectiveFrom         time.Time
	EffectiveTo           *time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
	ChangeReason          string
	CreatedBy             uuid.UUID
	CreatedAt             time.Time
	IsLatest              bool
	IsEffective           bool
}

// StorageZoneAdjustmentListResult là kết quả trả về khi liệt kê danh sách lịch sử điều chỉnh giá Zone của Storage.
type StorageZoneAdjustmentListResult struct {
	ZoneID     uuid.UUID
	Items      []StorageZoneAdjustmentListItem
	HasMore    bool
	ObservedAt time.Time
}

// StorageEstimate là kết quả ước tính chi phí lưu trữ (Storage Quote Projection) cho một mức dung lượng nhất định.
type StorageEstimate struct {
	CapacityBytes             int64
	HourlyMicroUnits          int64
	Currency                  string
	PricingScheduleCode       string
	PricingScheduleID         uuid.UUID
	PricingScheduleVersionID  uuid.UUID
	PricingVersion            int
	PricingChecksum           string
	PricingEffectiveFrom      time.Time
	RateAdjustmentID          *uuid.UUID
	RateAdjustmentVersion     *int
	RateAdjustmentChecksum    *string
	RateAdjustmentNumerator   int64
	RateAdjustmentDenominator int64
	EstimatedAt               time.Time
}
