package dto

// ListPlansRequest đại diện cho các tham số lọc truy vấn danh sách Plan được gửi lên từ client
type ListPlansRequest struct {
	ServiceType string `form:"service_type"`     // Lọc theo loại dịch vụ (STORAGE | VM | MAIL)
	ZoneID      string `form:"zone_id"`          // Lọc theo ID của Zone tài nguyên (UUID)
	Status      string `form:"status"`           // Lọc theo trạng thái (ACTIVE | DEPRECATED)
	Limit       int    `form:"limit,default=10"` // Số lượng bản ghi trên một trang, mặc định là 10
	Cursor      string `form:"cursor"`           // Con trỏ phân trang dạng Cursor (Base64)
}
