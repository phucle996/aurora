package entity

import (
	"time"

	"github.com/google/uuid"
)

// [COMMENT]: Tier đại diện cho một nấc cước biểu giá chi tiết (Flat Entity).
// Đã nhập 2 bảng tiers và tier_ranges làm 1 thực thể phẳng duy nhất, không chứa slice hay con trỏ lồng nhau.
// Domain Entity hoàn toàn độc lập, không dính bất kỳ tag JSON nào, các ID đều dạng uuid.UUID.
type Tier struct {
	ID            uuid.UUID // ID của nấc cước cụ thể (Range ID)
	TierID        uuid.UUID // ID của biểu giá gốc (Tier ID)
	Name          string    // Tên biểu giá (VD: Standard Storage Base Tier)
	Code          string    // Mã biểu giá (VD: STORAGE_STD_BASE)
	ServiceType   string    // Loại dịch vụ (VD: STORAGE | NETWORK_IN | NETWORK_OUT)
	Version       int       // OCC token của parent Tier
	RangeStart    int64     // Mốc bắt đầu tính bằng Megabytes (MB)
	RangeEnd      int64     // Mốc kết thúc (MB), 0 = không giới hạn
	BaseUnitPrice int64     // Giá gốc (USD Micro-units/MB/Hour)
	CreatedAt     time.Time // Thời điểm tạo của nấc cước
	UpdatedAt     time.Time // Thời điểm cập nhật biểu giá gốc
}

// TierRangeInput là trạng thái mong muốn của một range trong aggregate update.
// ID bằng uuid.Nil biểu thị range mới và sẽ được repository cấp UUID.
type TierRangeInput struct {
	ID            uuid.UUID
	RangeStart    int64
	RangeEnd      int64
	BaseUnitPrice int64
}

// TierUpdate chứa toàn bộ dữ liệu mutable của một Tier cùng khóa lookup bất biến.
type TierUpdate struct {
	Code        string
	ServiceType string
	Version     int
	Name        string
	Ranges      []TierRangeInput
}

// TierAggregate là snapshot trả về sau khi transaction update commit thành công.
type TierAggregate struct {
	ID          uuid.UUID
	Code        string
	ServiceType string
	Version     int
	Name        string
	Ranges      []TierRangeInput
	UpdatedAt   time.Time
}
