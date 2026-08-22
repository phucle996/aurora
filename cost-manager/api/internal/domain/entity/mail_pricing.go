package entity

import (
	"time"

	"github.com/google/uuid"
)

const ChargeKindMailAcceptedRecipient ChargeKindCode = "mail.delivery.accepted_recipient"

// MailPricingSnapshot là snapshot bất biến của bảng giá Mail Delivery cơ sở.
type MailPricingSnapshot struct {
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
	Brackets          []MailPricingSnapshotBracket
}

// MailPricingSnapshotBracket là bracket phân bậc trong Snapshot giá Mail.
type MailPricingSnapshotBracket struct {
	ID                       uuid.UUID
	RangeStartQuantity       int64
	RangeEndQuantity         *int64
	PriceNumeratorMicroUnits int64
	PriceDenominatorQuantity int64
}

// MailBasePricePublishCommand là Command phát hành phiên bản giá Mail Delivery mới.
type MailBasePricePublishCommand struct {
	ScheduleCode          string
	ExpectedLatestVersion int
	EffectiveFrom         time.Time
	ChangeReason          string
	CreatedBy             uuid.UUID
	Checksum              string
}

// MailBasePriceBracketCommand là bracket phân bậc đầu vào khi phát hành giá Mail.
type MailBasePriceBracketCommand struct {
	RangeStartQuantity       int64
	RangeEndQuantity         *int64
	PriceNumeratorMicroUnits int64
	PriceDenominatorQuantity int64
}

// MailBasePricePublishTarget là flat authority projection khi phát hành giá cơ sở Mail.
type MailBasePricePublishTarget struct {
	PricingScheduleID uuid.UUID
	ScheduleCode      string
	ChargeKindCode    ChargeKindCode
	PricingModel      PricingModel
	Currency          string
}

// MailBasePricePublished là kết quả sau khi phát hành phiên bản giá cơ sở Mail thành công.
type MailBasePricePublished struct {
	ID                uuid.UUID
	PricingScheduleID uuid.UUID
	ChargeKindCode    ChargeKindCode
	VersionNumber     int
	PricingModel      PricingModel
	Status            string
	EffectiveFrom     time.Time
	Checksum          string
}

// MailZoneAdjustmentPublishCommand là Command phát hành hệ số điều chỉnh giá theo Zone cho Mail Delivery.
type MailZoneAdjustmentPublishCommand struct {
	ZoneID                uuid.UUID
	ExpectedLatestVersion int
	EffectiveFrom         time.Time
	ChangeReason          string
	CreatedBy             uuid.UUID
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

// MailZoneAdjustmentPublished là kết quả sau khi phát hành hệ số điều chỉnh Zone cho Mail.
type MailZoneAdjustmentPublished struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	Status                string
	EffectiveFrom         time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

// MailZoneAdjustmentSnapshot là snapshot bất biến của hệ số điều chỉnh giá Zone cho Mail.
type MailZoneAdjustmentSnapshot struct {
	ID                    uuid.UUID
	ZoneID                uuid.UUID
	VersionNumber         int
	EffectiveFrom         time.Time
	MultiplierNumerator   int64
	MultiplierDenominator int64
	Checksum              string
}

// MailZoneAdjustmentListQuery là tham số truy vấn danh sách lịch sử điều chỉnh giá Zone cho Mail.
type MailZoneAdjustmentListQuery struct {
	ZoneID uuid.UUID
	Limit  int
}

// MailZoneAdjustmentListItem là thông tin tóm tắt 1 phiên bản điều chỉnh Zone của Mail.
type MailZoneAdjustmentListItem struct {
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

// MailZoneAdjustmentListResult là kết quả trả về khi liệt kê danh sách lịch sử điều chỉnh giá Zone của Mail.
type MailZoneAdjustmentListResult struct {
	ZoneID     uuid.UUID
	Items      []MailZoneAdjustmentListItem
	HasMore    bool
	ObservedAt time.Time
}

// MailEstimate là kết quả ước tính chi phí gửi thư (Mail Quote Projection).
type MailEstimate struct {
	RecipientQuantity         int64
	EstimateMicroUnits        int64
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
