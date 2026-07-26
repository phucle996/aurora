package entity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: ServiceType định nghĩa kiểu dữ liệu enum cho loại tài nguyên/dịch vụ tính cước.
type ServiceType string

const (
	ServiceTypeStorage    ServiceType = "STORAGE"     // Dịch vụ lưu trữ dữ liệu (MB)
	ServiceTypeNetworkIn  ServiceType = "NETWORK_IN"  // Băng thông mạng inbound truyền vào (MB)
	ServiceTypeNetworkOut ServiceType = "NETWORK_OUT" // Băng thông mạng outbound truyền ra (MB)
	ServiceTypeVM         ServiceType = "VM"          // Dịch vụ máy chủ ảo tính theo giờ
)

// [COMMENT]: Tier là flat read row từ parent metadata + pricing version có hiệu lực + immutable range.
// Domain Entity hoàn toàn độc lập, không dính bất kỳ tag JSON nào, các ID đều dạng uuid.UUID.
type Tier struct {
	ID              uuid.UUID   // ID của nấc cước cụ thể (Range ID)
	TierID          uuid.UUID   // ID của biểu giá gốc (Tier ID)
	Name            string      // Tên biểu giá (VD: Standard Storage Base Tier)
	Code            string      // Mã biểu giá (VD: STORAGE_STD_BASE)
	ServiceType     ServiceType // Loại dịch vụ (VD: STORAGE | NETWORK_IN | NETWORK_OUT)
	MetadataVersion int         // OCC token dành riêng cho display metadata
	PricingVersion  int         // Immutable pricing version đang hiển thị
	RangeStart      int64       // Mốc bắt đầu tính bằng Megabytes (MB)
	RangeEnd        int64       // Mốc kết thúc (MB), 0 = không giới hạn
	BaseUnitPrice   int64       // Giá gốc (USD Micro-units/MB/Hour)
	CreatedAt       time.Time   // Thời điểm tạo của nấc cước
	UpdatedAt       time.Time   // Thời điểm cập nhật biểu giá gốc
}

// TierRangeInput là một range thuộc immutable pricing snapshot mới.
type TierRangeInput struct {
	ID            uuid.UUID
	RangeStart    int64
	RangeEnd      int64
	BaseUnitPrice int64
}

// TierMetadataUpdate chỉ thay display name; không tạo pricing version hay outbox.
type TierMetadataUpdate struct {
	Code            string
	ServiceType     ServiceType
	MetadataVersion int
	Name            string
}

// TierVersionCreate chứa full-state ranges cho một append-only pricing version.
type TierVersionCreate struct {
	Code                  string
	ServiceType           ServiceType
	ExpectedLatestVersion int
	EffectiveFrom         time.Time
	ChangeReason          string
	CreatedBy             uuid.UUID
	Checksum              string
	Ranges                []TierRangeInput
}

// TierMetadata là snapshot metadata trả về sau update.
type TierMetadata struct {
	ID              uuid.UUID
	Code            string
	ServiceType     ServiceType
	MetadataVersion int
	Name            string
	UpdatedAt       time.Time
}

// TierVersion là immutable pricing snapshot trả về sau khi publish.
type TierVersion struct {
	ID            uuid.UUID
	TierID        uuid.UUID
	VersionNumber int
	Status        string
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	Checksum      string
	Ranges        []TierRangeInput
}

// TierDetail là aggregate đầy đủ để UI Edit không suy diễn từ flat paginated rows.
type TierDetail struct {
	ID              uuid.UUID
	Code            string
	ServiceType     ServiceType
	Name            string
	MetadataVersion int
	LatestVersion   TierVersion
}

// PricingSnapshot là immutable pricing read model dùng chung cho estimate và các read path nhanh.
// Không chứa owner/request data; giá trị được chọn theo effective window trong Billing PostgreSQL.
type PricingSnapshot struct {
	TierID        uuid.UUID
	TierVersionID uuid.UUID
	Code          string
	ServiceType   ServiceType
	VersionNumber int
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	Checksum      string
	Currency      string
	Ranges        []TierRangeInput
}

// StorageEstimate là kết quả ước tính tham khảo, không phải ledger posting hay authorization decision.
type StorageEstimate struct {
	CapacityBytes        int64
	HourlyMicroUnits     int64
	MonthlyMicroUnits    int64
	BillingHoursPerMonth int64
	Currency             string
	TierCode             string
	TierID               uuid.UUID
	TierVersionID        uuid.UUID
	PricingVersion       int
	PricingChecksum      string
	PricingEffectiveFrom time.Time
	EstimatedAt          time.Time
}
