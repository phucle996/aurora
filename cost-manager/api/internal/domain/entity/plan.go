package entity

import (
	"time"

	"github.com/google/uuid"
)

// Danh sách trạng thái của Plan
const (
	PlanStatusActive     = "ACTIVE"     // Plan đang hoạt động bình thường
	PlanStatusDeprecated = "DEPRECATED" // Plan đã ngưng sử dụng
)

// Plan đại diện cho Resource SKU Plan trong hệ thống (bảng billing.plans)
type Plan struct {
	ID           uuid.UUID   // Khóa chính UUID
	Name         string      // Tên hiển thị của plan (VD: Storage Standard 10GB)
	Code         string      // Mã code duy nhất (VD: STORAGE_SKU_10GB)
	ServiceType  ServiceType // Loại dịch vụ (STORAGE | VM)
	ZoneID       uuid.UUID   // Khóa ngoại tham chiếu đến Zone UUID cụ thể
	MonthlyPrice int64       // Đơn giá gốc lẻ của SKU hàng tháng (USD Micro-units)
	Currency     string      // Đơn vị tiền tệ (mặc định: USD)
	Status       string      // Trạng thái (ACTIVE | DEPRECATED)
	Description  string      // Mô tả chi tiết về Plan
	CreatedAt    time.Time   // Thời gian khởi tạo
	UpdatedAt    time.Time   // Thời gian cập nhật cuối cùng
}
